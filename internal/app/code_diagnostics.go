package app

import (
	"context"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

// diagnosticsRoute validates the stable backend identity, not the SSH alias used
// by file editing. Optional support fails closed until CD-09 negotiates it.
func (a *App) diagnosticsRoute(expectedHostID string) (engine.CodeDiagnosticsBackend, uint64, error) {
	var gen uint64
	if a.conn != nil {
		gen = a.conn.generation()
	}
	r := a.route()
	if _, offline := r.backend.(offlineBackend); offline {
		return nil, 0, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
	}
	b, ok := r.backend.(engine.CodeDiagnosticsBackend)
	identity, hasIdentity := r.backend.(interface {
		CodeDiagnosticsHostID(context.Context) (string, error)
	})
	if !ok || !hasIdentity {
		return nil, 0, &cd.Error{Code: "unsupported", Message: "Code diagnostics are unavailable on this backend"}
	}
	host, err := identity.CodeDiagnosticsHostID(a.appCtx())
	if err != nil {
		return nil, 0, err
	}
	if host != expectedHostID {
		return nil, 0, &cd.Error{Code: "staleContext", Message: "Selected host changed"}
	}
	if err := a.diagnosticsCurrent(gen); err != nil {
		return nil, 0, err
	}
	return b, gen, nil
}

func (a *App) diagnosticsCurrent(gen uint64) error {
	if a.conn != nil && a.conn.generation() != gen {
		return &cd.Error{Code: "staleContext", Message: "Connection changed during request"}
	}
	return nil
}

func (a *App) AttachCodeDiagnostics(dir string, contractVersion int, expectedHostID string) (engine.CodeDiagnosticsAttachment, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsAttachment{}, err
	}
	result, err := b.AttachCodeDiagnostics(a.appCtx(), dir, contractVersion)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		if err == nil {
			_, _ = b.DetachCodeDiagnostics(context.Background(), result.Context)
		}
		return engine.CodeDiagnosticsAttachment{}, stale
	}
	return result, err
}

func (a *App) GetCodeDiagnosticsSnapshot(c cd.Context, expectedHostID string) (cd.Snapshot, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return cd.Snapshot{}, err
	}
	s, err := b.GetCodeDiagnosticsSnapshot(a.appCtx(), c)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return cd.Snapshot{}, stale
	}
	return s, err
}

func (a *App) ApproveCodeDiagnostics(c cd.Context, mode, fingerprint, expectedHostID string) (cd.Snapshot, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return cd.Snapshot{}, err
	}
	s, err := b.ApproveCodeDiagnostics(a.appCtx(), c, mode, fingerprint)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return cd.Snapshot{}, stale
	}
	return s, err
}

func (a *App) RevokeCodeDiagnostics(c cd.Context, expectedHostID string) (cd.Snapshot, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return cd.Snapshot{}, err
	}
	s, err := b.RevokeCodeDiagnostics(a.appCtx(), c)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return cd.Snapshot{}, stale
	}
	return s, err
}

func (a *App) RunCodeDiagnostics(c cd.Context, providerIDs []string, expectedHostID string) (engine.CodeDiagnosticsRuns, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsRuns{}, err
	}
	s, err := b.RunCodeDiagnostics(a.appCtx(), c, providerIDs)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return engine.CodeDiagnosticsRuns{}, stale
	}
	return s, err
}

func (a *App) CancelCodeDiagnostics(c cd.Context, runs []cd.RunIdentity, expectedHostID string) (engine.CodeDiagnosticsAck, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsAck{}, err
	}
	s, err := b.CancelCodeDiagnostics(a.appCtx(), c, runs)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return engine.CodeDiagnosticsAck{}, stale
	}
	return s, err
}

func (a *App) DetachCodeDiagnostics(c cd.Context, expectedHostID string) (engine.CodeDiagnosticsAck, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsAck{}, err
	}
	s, err := b.DetachCodeDiagnostics(a.appCtx(), c)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return engine.CodeDiagnosticsAck{}, stale
	}
	return s, err
}
