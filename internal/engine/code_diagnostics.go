package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// CodeDiagnosticsBackend is an optional Stage 1 surface. RPC negotiation belongs
// to CD-09; implementing Backend alone never advertises checker support.
type CodeDiagnosticsBackend interface {
	AttachCodeDiagnostics(context.Context, string, int) (CodeDiagnosticsAttachment, error)
	DetachCodeDiagnostics(context.Context, cd.Context) (CodeDiagnosticsAck, error)
	GetCodeDiagnosticsSnapshot(context.Context, cd.Context) (cd.Snapshot, error)
	ApproveCodeDiagnostics(context.Context, cd.Context, string, string) (cd.Snapshot, error)
	RevokeCodeDiagnostics(context.Context, cd.Context) (cd.Snapshot, error)
	RunCodeDiagnostics(context.Context, cd.Context, []string) (CodeDiagnosticsRuns, error)
	CancelCodeDiagnostics(context.Context, cd.Context, []cd.RunIdentity) (CodeDiagnosticsAck, error)
}
type CodeDiagnosticsAttachment struct {
	Project  cd.Project  `json:"project"`
	Context  cd.Context  `json:"context"`
	Snapshot cd.Snapshot `json:"snapshot"`
}
type (
	CodeDiagnosticsAck struct {
		Context cd.Context `json:"context"`
	}
	CodeDiagnosticsRuns struct {
		Context cd.Context       `json:"context"`
		Runs    []cd.RunIdentity `json:"runs"`
	}
)

const CodeDiagnosticsUpdatedEvent = "codeDiagnostics:updated"

// diagnosticsOwner has a lifetime independent of any individual request. No
// callbacks or process joins occur under Engine.mu.
type diagnosticsOwner struct {
	manager *cd.Manager
	live    *cd.LiveDocuments
	// Internal route only: explicit reviewed discovery + launcher injection.
	// Production leaves both nil pending the live release gates.
	discoverLive func(cd.AuthorizedRoot) ([]cd.Registration, error)
	roots        map[string]cd.AuthorizedRoot
	host         string
	mu           sync.Mutex
	contexts     map[string]cd.Context
	stop         chan struct{}
	done         chan struct{}
	workers      sync.WaitGroup
}

func diagnosticError(code, message string) error { return &cd.Error{Code: code, Message: message} }
func newDiagnosticsOwner(config string, host string) (*diagnosticsOwner, error) {
	return newDiagnosticsOwnerWithLive(config, host, nil, cd.SessionOptions{})
}

// The internal route requires explicitly supplied reviewed host metadata and a
// launcher. Production deliberately calls the checker-only constructor above.
func newDiagnosticsOwnerWithLive(config, host string, discover func(cd.AuthorizedRoot) ([]cd.Registration, error), options cd.SessionOptions) (*diagnosticsOwner, error) {
	store, err := cd.NewGrantStore(config)
	if err != nil {
		return nil, err
	}
	trust := cd.NewTrust(store)
	var id [16]byte
	_, _ = rand.Read(id[:])
	manager, err := cd.NewManager(hex.EncodeToString(id[:]), trust, cd.NewSupervisor(trust, cd.SupervisorOptions{}), cd.ManagerLimits{})
	if err != nil {
		return nil, err
	}
	o := &diagnosticsOwner{manager: manager, host: host, roots: map[string]cd.AuthorizedRoot{}, contexts: map[string]cd.Context{}, stop: make(chan struct{}), done: make(chan struct{})}
	if discover != nil && options.Launch != nil {
		o.discoverLive = discover
		o.live = cd.NewLiveDocuments(trust, manager, options)
		manager.SetDocumentProvider(o.live)
	}
	return o, nil
}

func (e *Engine) diagnostics(ctx context.Context) (*diagnosticsOwner, error) {
	if err := ctx.Err(); err != nil {
		return nil, diagnosticError("canceled", "Request canceled")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, diagnosticError("disconnected", "Engine is closed")
	}
	if e.codeDiagnostics != nil {
		return e.codeDiagnostics, nil
	}
	roots := e.roots
	if roots.Config == "" {
		roots, _ = paths.Default()
	}
	host, err := localDiagnosticsIdentity(roots.Config)
	if err != nil {
		return nil, err
	}
	owner, err := newDiagnosticsOwner(roots.Config, host)
	if err != nil {
		return nil, err
	}
	e.codeDiagnostics = owner
	go owner.events(e)
	return owner, nil
}

func (o *diagnosticsOwner) events(e *Engine) {
	defer close(o.done)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-o.stop:
			return
		case <-ticker.C:
			if o.live != nil {
				o.live.Refresh()
			}
			o.mu.Lock()
			contexts := make([]cd.Context, 0, len(o.contexts))
			for _, c := range o.contexts {
				contexts = append(contexts, c)
			}
			o.mu.Unlock()
			for _, c := range contexts {
				updates, gap, err := o.manager.DrainEvents(c)
				if err != nil {
					continue
				}
				// A full snapshot on a gap preserves the contract's sequence-gap recovery:
				// clients see the jump and resnapshot rather than a successful empty update.
				if gap {
					parts, err := o.manager.SnapshotEvents(c)
					if err == nil {
						updates = [][]cd.Event{parts}
					}
				}
				for _, parts := range updates {
					for _, part := range parts {
						current, err := o.manager.GetSnapshot(c)
						if err != nil || current.Context != part.Context {
							break
						}
						e.mu.Lock()
						emitter := e.emitter
						active := e.codeDiagnostics == o && !e.closed
						e.mu.Unlock()
						if active && emitter != nil {
							emitter.Emit(CodeDiagnosticsUpdatedEvent, part)
						}
					}
				}
			}
		}
	}
}

func (o *diagnosticsOwner) close() {
	close(o.stop)
	o.manager.Close()
	o.workers.Wait()
	if o.live != nil {
		o.live.Close()
	}
	<-o.done
}

// AuthorizeCodeDiagnosticsRoot checks catalog membership before canonicalization
// or metadata discovery. Tokens and arbitrary paths are never authority.
func (s *Service) AuthorizeCodeDiagnosticsRoot(ctx context.Context, host, dir string) (cd.AuthorizedRoot, error) {
	if ctx.Err() != nil {
		return cd.AuthorizedRoot{}, diagnosticError("canceled", "Request canceled")
	}
	return cd.AuthorizeRoot(host, dir, s.knownDirectory)
}

func (e *Engine) AttachCodeDiagnostics(ctx context.Context, dir string, version int) (CodeDiagnosticsAttachment, error) {
	if version != 1 {
		return CodeDiagnosticsAttachment{}, diagnosticError("unsupported", "Unsupported diagnostics contract")
	}
	// Reject unknown roots even before host identity initialization touches disk.
	if !e.svc.knownDirectory(dir) {
		return CodeDiagnosticsAttachment{}, diagnosticError("unauthorized", "Project directory is not in the catalog")
	}
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsAttachment{}, err
	}
	root, err := e.svc.AuthorizeCodeDiagnosticsRoot(ctx, o.host, dir)
	if err != nil {
		return CodeDiagnosticsAttachment{}, err
	}
	// Serialize Add with Close via Engine.mu; discovery never blocks attach.
	e.mu.Lock()
	if e.closed || e.codeDiagnostics != o {
		e.mu.Unlock()
		return CodeDiagnosticsAttachment{}, diagnosticError("disconnected", "Engine is closed")
	}
	s, err := o.manager.Attach(root, version, nil)
	if err == nil {
		o.mu.Lock()
		o.contexts[s.Context.AttachmentID] = s.Context
		o.roots[s.Context.AttachmentID] = root
		o.mu.Unlock()
		o.workers.Add(1)
		go func() {
			defer o.workers.Done()
			registrations := discoverCheckers(root)
			if o.discoverLive != nil {
				live, discoveryErr := o.discoverLive(root)
				if discoveryErr == nil {
					registrations = append(registrations, live...)
					discoveryErr = o.live.Register(root, registrations)
				}
				if discoveryErr != nil {
					registrations = []cd.Registration{{ProviderID: "gopls", Availability: "unsupported"}, {ProviderID: "typescript", Availability: "unsupported"}, {ProviderID: "svelte", Availability: "unsupported"}}
				}
			}
			if err := o.manager.CompleteDiscovery(s.Context, registrations); err != nil {
				// Combined proposals may exceed the contract's tool/hash budget.
				// Fail closed with explicit unavailable metadata, never leave an
				// apparently pending executable proposal or silently enable a subset.
				_ = o.manager.CompleteDiscovery(s.Context, []cd.Registration{
					{ProviderID: "go-vet", Availability: "unsupported"},
					{ProviderID: "svelte-check", Availability: "unsupported"},
				})
			}
		}()
	}
	e.mu.Unlock()
	if err != nil {
		return CodeDiagnosticsAttachment{}, err
	}
	return CodeDiagnosticsAttachment{root.Project(), s.Context, s}, nil
}

func discoverCheckers(root cd.AuthorizedRoot) []cd.Registration {
	home, _ := os.UserHomeDir()
	cache, _ := os.UserCacheDir()
	path := filepath.SplitList(os.Getenv("PATH"))
	register := func(id string, adapter cd.CheckerAdapter, err error) cd.Registration {
		if err == nil {
			return cd.Registration{ProviderID: id, Availability: "available", Adapter: adapter}
		}
		availability := "unsupported"
		var failure *cd.Error
		if errors.As(err, &failure) && failure.Code == "notFound" {
			availability = "missingTool"
		}
		return cd.Registration{ProviderID: id, Availability: availability}
	}
	goPlan, goErr := cd.DiscoverGoVet(root, path, cd.GoVetEnvironment{Home: home, Cache: filepath.Join(cache, "go-build"), ModuleCache: filepath.Join(home, "go", "pkg", "mod")})
	svelte, svelteErr := cd.DiscoverSvelte(root, path, home)
	return []cd.Registration{register("go-vet", goPlan, goErr), register("svelte-check", svelte, svelteErr)}
}

func (e *Engine) GetCodeDiagnosticsSnapshot(ctx context.Context, c cd.Context) (cd.Snapshot, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return cd.Snapshot{}, err
	}
	if o.live != nil {
		o.live.Refresh()
	}
	return o.manager.GetSnapshot(c)
}

func (e *Engine) ApproveCodeDiagnostics(ctx context.Context, c cd.Context, mode, fingerprint string) (cd.Snapshot, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return cd.Snapshot{}, err
	}
	return o.manager.Approve(c, fingerprint, mode)
}

func (e *Engine) RevokeCodeDiagnostics(ctx context.Context, c cd.Context) (cd.Snapshot, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return cd.Snapshot{}, err
	}
	s, err := o.manager.Revoke(c)
	if s.Context.AttachmentID != "" {
		if o.live != nil {
			o.mu.Lock()
			root := o.roots[c.AttachmentID]
			o.mu.Unlock()
			o.live.Revoke(root)
		}
		o.mu.Lock()
		if o.contexts[c.AttachmentID] == c {
			o.contexts[c.AttachmentID] = s.Context
		}
		o.mu.Unlock()
	}
	return s, err
}

func (e *Engine) DetachCodeDiagnostics(ctx context.Context, c cd.Context) (CodeDiagnosticsAck, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsAck{}, err
	}
	err = o.manager.Detach(c)
	if err == nil {
		o.mu.Lock()
		delete(o.contexts, c.AttachmentID)
		delete(o.roots, c.AttachmentID)
		o.mu.Unlock()
	}
	return CodeDiagnosticsAck{c}, err
}

// Retry is Run with a single provider ID. In-flight work coalesces; completed
// work gets a fresh ID. Never retry a mutation across a new connection.
func (e *Engine) RunCodeDiagnostics(ctx context.Context, c cd.Context, ids []string) (CodeDiagnosticsRuns, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsRuns{}, err
	}
	runs, err := o.manager.Run(c, ids, false)
	return CodeDiagnosticsRuns{c, runs}, err
}

func (e *Engine) CancelCodeDiagnostics(ctx context.Context, c cd.Context, runs []cd.RunIdentity) (CodeDiagnosticsAck, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsAck{}, err
	}
	return CodeDiagnosticsAck{c}, o.manager.Cancel(c, runs)
}

var _ CodeDiagnosticsBackend = (*Engine)(nil)
