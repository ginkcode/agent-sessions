package rpc

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// dispatchManagedTool forwards to the engine on this host, which installs into
// its own app data directory. It requires the host identity this server
// computed at initialize (initializeDiagnostics); the engine checks the request
// against it again, so neither a picker label nor a client assertion counts.
// The request names a catalog tool and opaque consent/job IDs only.
func (s *Server) dispatchManagedTool(ctx context.Context, req Request) (any, error) {
	if len(req.Params) > 2048 {
		return nil, cdError("invalidArgument", "Managed tool request exceeds limit")
	}
	var p engine.ManagedToolRequest
	decoder := json.NewDecoder(bytes.NewReader(req.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, cdError("invalidArgument", "Invalid managed tool parameters")
	}
	s.diagnostics.mu.Lock()
	host, closed := s.diagnostics.host, s.diagnostics.closed
	s.diagnostics.mu.Unlock()
	if closed {
		return nil, cdError("disconnected", "Connection closed")
	}
	b, ok := s.backend.(engine.ManagedToolsBackend)
	if host == "" || !ok {
		return nil, cdError("unsupported", "Managed installation is unavailable on this host")
	}
	if p.ExpectedHostID != host {
		return nil, cdError("staleContext", "Actual host identity required")
	}
	return b.ManagedTool(ctx, p)
}

// ManagedTool runs on the connected host. A server without a verified
// diagnostics identity (including an older one) is unsupported; there is no
// fallback to another host.
func (c *Client) ManagedTool(ctx context.Context, p engine.ManagedToolRequest) (engine.ManagedToolState, error) {
	host, err := c.CodeDiagnosticsHostID(ctx)
	if err != nil {
		return engine.ManagedToolState{}, err
	}
	if p.ExpectedHostID != host {
		return engine.ManagedToolState{}, cdError("staleContext", "Selected host changed")
	}
	var out engine.ManagedToolState
	if err = normalizeDiagnosticsError(c.call(ctx, "managedTool", p, &out)); err != nil {
		return engine.ManagedToolState{}, err
	}
	if out.HostID != host {
		return engine.ManagedToolState{}, cdError("unauthorized", "Backend host identity changed")
	}
	return out, nil
}
