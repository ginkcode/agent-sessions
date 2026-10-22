package app

import (
	"context"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

// ManagedTool is an optional, host-epoch checked operation. The request contains
// only the closed tool ID and opaque consent/job IDs, never installer policy.
func (a *App) ManagedTool(p engine.ManagedToolRequest) (engine.ManagedToolState, error) {
	var gen uint64
	if a.conn != nil {
		gen = a.conn.generation()
	}
	r := a.route()
	if _, offline := r.backend.(offlineBackend); offline {
		return engine.ManagedToolState{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
	}
	b, ok := r.backend.(engine.ManagedToolsBackend)
	identity, hasIdentity := r.backend.(interface {
		CodeDiagnosticsHostID(context.Context) (string, error)
	})
	if !ok || !hasIdentity {
		return engine.ManagedToolState{}, &cd.Error{Code: "unsupported", Message: "Managed tool installation is unavailable on this backend"}
	}
	host, err := identity.CodeDiagnosticsHostID(a.appCtx())
	if err != nil {
		return engine.ManagedToolState{}, err
	}
	if host == "" || host != p.ExpectedHostID {
		return engine.ManagedToolState{}, &cd.Error{Code: "staleContext", Message: "Selected host changed"}
	}
	if err = a.diagnosticsCurrent(gen); err != nil {
		return engine.ManagedToolState{}, err
	}
	out, err := b.ManagedTool(a.appCtx(), p)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		// Cancel an acknowledged job on the ORIGINAL backend, never the new route.
		if out.JobID != "" {
			_, _ = b.ManagedTool(context.Background(), engine.ManagedToolRequest{Action: "cancel", ExpectedHostID: host, JobID: out.JobID})
		}
		return engine.ManagedToolState{}, stale
	}
	return out, err
}
