package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	mt "github.com/ginkcode/agent-sessions/internal/managedtools"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

type managedInstaller interface {
	Propose(string) (mt.Proposal, error)
	Dismiss()
	Install(context.Context, mt.Binding, string) (string, error)
	Discover(string) (string, error)
	Dirs() []string
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

// appRoots fills in this host's default config and data directories.
func (e *Engine) appRoots() (paths.Roots, error) {
	roots := e.roots
	if roots.Config == "" || roots.Data == "" {
		defaults, err := paths.Default()
		if err != nil {
			return roots, diagnosticError("unsupported", "App directories unavailable")
		}
		if roots.Config == "" {
			roots.Config = defaults.Config
		}
		if roots.Data == "" {
			roots.Data = defaults.Data
		}
	}
	return roots, nil
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
	if e.liveInjection != nil && e.liveInjection.Host != "" {
		return nil, diagnosticError("unsupported", "Managed installation is unavailable for an injected host")
	}
	roots, err := e.appRoots()
	if err != nil {
		return nil, err
	}
	host, err := diagnosticsIdentity(e.hostKind(), roots.Config, e.machineID)
	if err != nil {
		return nil, err
	}
	var id [32]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, errors.New("managed tools: connection unavailable")
	}
	binding := mt.Binding{HostID: host, ConnectionID: hex.EncodeToString(id[:])}
	installer, err := mt.New(roots.Data, binding, e.hostGoRoot)
	if err != nil {
		return nil, err
	}
	installer.UseHostNode(hostNode)
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

// managedDirs lists the directories of valid managed installs on this host,
// searched after the user's own. Read-only; empty when nothing is installed.
func (e *Engine) managedDirs() []string {
	o, err := e.managedOwner(context.Background())
	if err != nil {
		return nil
	}
	return o.installer.Dirs()
}
