package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	mt "github.com/ginkcode/agent-sessions/internal/managedtools"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// WithReviewedManagedGo provisions an explicitly reviewed installed tree through
// trusted backend composition. Never deserialize this option from client input.
// No default/PATH/toolchain download fallback exists; production remains unwired.
func WithReviewedManagedGo(goTree mt.ReviewedGo) Option {
	return func(e *Engine) { e.managedGo = &goTree }
}

type managedInstaller interface {
	Propose(string) (mt.Proposal, error)
	Dismiss()
	Install(context.Context, mt.Binding, string) (string, error)
	Discover() (string, error)
}

type managedToolsOwner struct {
	installer managedInstaller
	binding   mt.Binding
	ctx       context.Context
	cancel    context.CancelFunc
	workers   sync.WaitGroup // Add serialized with removal under Engine.mu
	pending   *mt.Proposal
	job       *managedJob
}

func (o *managedToolsOwner) close() {
	o.cancel()
	o.installer.Dismiss()
	o.workers.Wait()
}

func (e *Engine) managedOwner(ctx context.Context) (*managedToolsOwner, error) {
	if ctx.Err() != nil {
		return nil, diagnosticError("canceled", "Request canceled")
	}
	// Host identity is backend-local, not a picker label or an injected remote ID.
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, diagnosticError("disconnected", "Engine is closed")
	}
	if e.managedTools != nil {
		return e.managedTools, nil
	}
	if e.managedGo == nil {
		return nil, mt.ErrPrerequisite
	}
	if e.liveInjection != nil && e.liveInjection.Host != "" {
		return nil, diagnosticError("unsupported", "Managed remote installation is unavailable")
	}
	roots := e.roots
	if roots.Config == "" || roots.Data == "" {
		defaults, err := paths.Default()
		if err != nil {
			return nil, diagnosticError("unsupported", "Local app directories unavailable")
		}
		if roots.Config == "" {
			roots.Config = defaults.Config
		}
		if roots.Data == "" {
			roots.Data = defaults.Data
		}
	}
	host, err := localDiagnosticsIdentity(roots.Config, e.machineID)
	if err != nil {
		return nil, err
	}
	var id [32]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, errors.New("managed tools: connection unavailable")
	}
	binding := mt.Binding{HostID: host, ConnectionID: hex.EncodeToString(id[:])}
	installer, err := mt.NewReviewed(roots.Data, binding, *e.managedGo)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	o := &managedToolsOwner{installer: installer, binding: binding, ctx: lifetime, cancel: cancel}
	e.managedTools = o
	return o, nil
}

// These are backend-only seams, not optional RPC/App contracts (TI-03).
func (e *Engine) ProposeManagedTool(ctx context.Context, tool string) (mt.Proposal, error) {
	o, err := e.managedOwner(ctx)
	if err != nil {
		return mt.Proposal{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.managedTools != o {
		return mt.Proposal{}, diagnosticError("disconnected", "Connection changed")
	}
	return o.installer.Propose(tool)
}

func (e *Engine) InstallManagedTool(ctx context.Context, token string) (string, error) {
	o, err := e.managedOwner(ctx)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	if e.managedTools != o {
		e.mu.Unlock()
		return "", diagnosticError("disconnected", "Connection changed")
	}
	o.workers.Add(1)
	e.mu.Unlock()
	defer o.workers.Done()
	return e.installManagedOwned(ctx, o, token)
}

func (e *Engine) installManagedOwned(ctx context.Context, o *managedToolsOwner, token string) (string, error) {
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.ctx, cancel)
	defer stop()
	defer cancel()
	path, err := o.installer.Install(request, o.binding, token)
	if err != nil {
		return "", err
	}
	// Drop ALL old execution approvals/attachments in this owner before exposing
	// a newly discovered executable. Reattach recomputes the composite byte hash;
	// installation itself never supplies a grant or starts a server.
	e.mu.Lock()
	if e.managedTools != o || o.ctx.Err() != nil {
		e.mu.Unlock()
		return "", diagnosticError("disconnected", "Connection changed")
	}
	diagnostics := e.codeDiagnostics
	e.codeDiagnostics = nil
	e.mu.Unlock()
	if diagnostics != nil {
		diagnostics.close()
	}
	return path, nil
}

// RevokeManagedTools drops proposals, cancels owned process groups and joins work.
// Reconnect requires a new owner/nonce even though the stable local host is same.
func (e *Engine) RevokeManagedTools() {
	e.mu.Lock()
	o := e.managedTools
	e.managedTools = nil
	e.mu.Unlock()
	if o != nil {
		o.close()
	}
}

// ManagedToolAvailability is read-only metadata; it never offers a grant/probe.
func (e *Engine) ManagedToolAvailability(ctx context.Context) (string, error) {
	o, err := e.managedOwner(ctx)
	if err != nil {
		return "unsupported", err
	}
	_, err = o.installer.Discover()
	if os.IsNotExist(err) {
		return "missingTool", nil
	}
	if err != nil {
		return "unsupported", diagnosticError("unsupported", "Managed installation is invalid")
	}
	return "available", nil
}

// DiscoverManagedGopls supplies metadata only to an explicitly reviewed caller.
// Validates receipt AND binary before passing the private directory to adapter
// discovery; no fallback to inherited PATH and no production live flag change.
func (e *Engine) DiscoverManagedGopls(ctx context.Context, root cd.AuthorizedRoot, env cd.GoplsEnvironment) (*cd.GoplsPlan, error) {
	o, err := e.managedOwner(ctx)
	if err != nil {
		return nil, err
	}
	if root.Project().HostID != o.binding.HostID {
		return nil, diagnosticError("unsupported", "Authenticated local host required")
	}
	if e.managedGo == nil || !e.managedGo.MatchesRoot(env.GoRoot) {
		return nil, diagnosticError("unsupported", "Reviewed Go installation changed or unavailable")
	}
	executable, err := o.installer.Discover()
	if err != nil {
		return nil, diagnosticError("unsupported", "Managed gopls installation unavailable or invalid")
	}
	// Adapter adds executable bytes, fixed argv/env and workspace configuration to
	// the independent project execution fingerprint.
	return cd.DiscoverGopls(root, []string{filepath.Dir(executable)}, env)
}
