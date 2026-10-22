package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

// diagnosticsRPC serializes ownership changes and event admission, not transport
// writes. Queue exhaustion closes the connection rather than silently losing an
// update. Manager snapshots remain the recovery authority after sequence gaps.
type diagnosticsRPC struct {
	mu             sync.Mutex
	host           string
	owned          map[string]cd.Context
	closed         bool
	queue          chan Notification
	pendingBytes   int
	pendingUpdates map[string]map[string]int
}

type codeDiagnosticsParams struct {
	Dir             string           `json:"dir"`
	ContractVersion int              `json:"contractVersion"`
	Context         cd.Context       `json:"context"`
	Mode            string           `json:"mode"`
	Fingerprint     string           `json:"fingerprint"`
	ProviderIDs     []string         `json:"providerIds"`
	Runs            []cd.RunIdentity `json:"runs"`
}

func cdError(code, message string) error { return &cd.Error{Code: code, Message: message} }

// A headless connection may not reuse the local machine identity. Until the
// transport supplies verified SSH/WSL tuples, production remote support stays
// unavailable. In particular neither picker labels nor client assertions count.
func (s *Server) initializeDiagnostics(ctx context.Context, caps *Capabilities) string {
	caps.CodeDiagnostics, caps.LiveCodeDiagnostics = false, false
	if _, ok := s.backend.(engine.CodeDiagnosticsBackend); !ok {
		return ""
	}
	identity, ok := s.backend.(interface {
		RemoteCodeDiagnosticsHostID(context.Context) (string, error)
	})
	if !ok {
		return ""
	}
	host, err := identity.RemoteCodeDiagnosticsHostID(ctx)
	if err != nil || (!strings.HasPrefix(host, "ssh:") && !strings.HasPrefix(host, "wsl:")) {
		return ""
	}
	s.diagnostics.mu.Lock()
	s.diagnostics.host = host
	s.diagnostics.mu.Unlock()
	caps.CodeDiagnostics = true
	return host
}

func (s *Server) dispatchDiagnostics(ctx context.Context, req Request) (any, error) {
	d := &s.diagnostics
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, cdError("disconnected", "Connection closed")
	}
	if d.host == "" {
		return nil, cdError("unsupported", "Verified remote diagnostics identity is unavailable")
	}
	var p codeDiagnosticsParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, cdError("invalidArgument", "Invalid diagnostics parameters")
	}
	if req.Method == "attachCodeDiagnostics" {
		a, err := s.backend.(engine.CodeDiagnosticsBackend).AttachCodeDiagnostics(ctx, p.Dir, p.ContractVersion)
		if err != nil {
			return nil, err
		}
		if a.Project.HostID != d.host {
			_, _ = s.backend.(engine.CodeDiagnosticsBackend).DetachCodeDiagnostics(context.Background(), a.Context)
			return nil, cdError("unauthorized", "Backend host identity changed")
		}
		d.owned[a.Context.AttachmentID] = a.Context
		return a, nil
	}
	if c, ok := d.owned[p.Context.AttachmentID]; !ok || c != p.Context {
		return nil, cdError("staleContext", "Diagnostics context is not owned by this connection")
	}
	switch req.Method {
	case "detachCodeDiagnostics":
		out, err := s.backend.(engine.CodeDiagnosticsBackend).DetachCodeDiagnostics(ctx, p.Context)
		if err == nil {
			delete(d.owned, p.Context.AttachmentID)
		}
		return out, err
	case "getCodeDiagnosticsSnapshot":
		return s.backend.(engine.CodeDiagnosticsBackend).GetCodeDiagnosticsSnapshot(ctx, p.Context)
	case "approveCodeDiagnostics":
		return s.backend.(engine.CodeDiagnosticsBackend).ApproveCodeDiagnostics(ctx, p.Context, p.Mode, p.Fingerprint)
	case "revokeCodeDiagnostics":
		out, err := s.backend.(engine.CodeDiagnosticsBackend).RevokeCodeDiagnostics(ctx, p.Context)
		if out.Context.AttachmentID == p.Context.AttachmentID && out.Context.ConnectionID == p.Context.ConnectionID && out.Approval.State == "revoked" {
			d.owned[p.Context.AttachmentID] = out.Context
		}
		return out, err
	case "runCodeDiagnostics":
		return s.backend.(engine.CodeDiagnosticsBackend).RunCodeDiagnostics(ctx, p.Context, p.ProviderIDs)
	case "cancelCodeDiagnostics":
		return s.backend.(engine.CodeDiagnosticsBackend).CancelCodeDiagnostics(ctx, p.Context, p.Runs)
	}
	return nil, cdError("unsupported", "Unsupported diagnostics operation")
}

func (s *Server) notifyDiagnostics(payload any) error {
	ev, ok := payload.(cd.Event)
	if !ok {
		return cdError("invalidArgument", "Invalid diagnostics event")
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	n := Notification{JSONRPC: JSONRPCVersion, Method: engine.CodeDiagnosticsUpdatedEvent, Params: raw}
	frame, err := json.Marshal(n)
	if err != nil {
		return err
	}
	if len(frame)+1 > 1<<20 {
		_ = s.Close()
		return cdError("limitExceeded", "Diagnostics event envelope exceeds limit")
	}
	d := &s.diagnostics
	d.mu.Lock()
	if d.closed || ev.Context.AttachmentID == "" || d.owned[ev.Context.AttachmentID] != ev.Context {
		d.mu.Unlock()
		return nil
	}
	updates := d.pendingUpdates[ev.Context.AttachmentID]
	if updates == nil {
		updates = map[string]int{}
		d.pendingUpdates[ev.Context.AttachmentID] = updates
	}
	if (updates[ev.UpdateID] == 0 && len(updates) >= 2) || d.pendingBytes+len(frame) > 16<<20 {
		d.mu.Unlock()
		_ = s.Close()
		return cdError("disconnected", "Diagnostics delivery overflow")
	}
	d.pendingBytes += len(frame)
	updates[ev.UpdateID]++
	select {
	case d.queue <- n:
		d.mu.Unlock()
		return nil
	default:
		d.mu.Unlock()
		_ = s.Close()
		return cdError("disconnected", "Diagnostics peer exceeded delivery budget")
	}
}

func (s *Server) diagnosticsWriter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case n := <-s.diagnostics.queue:
			var ev cd.Event
			if json.Unmarshal(n.Params, &ev) != nil {
				continue
			}
			frame, _ := json.Marshal(n)
			s.diagnostics.mu.Lock()
			s.diagnostics.pendingBytes -= len(frame)
			updates := s.diagnostics.pendingUpdates[ev.Context.AttachmentID]
			updates[ev.UpdateID]--
			if updates[ev.UpdateID] <= 0 {
				delete(updates, ev.UpdateID)
			}
			valid := !s.diagnostics.closed && s.diagnostics.owned[ev.Context.AttachmentID] == ev.Context
			s.diagnostics.mu.Unlock()
			if valid {
				if err := s.writer.WriteFrame(n); err != nil {
					_ = s.Close()
					return
				}
			}
		}
	}
}

func (s *Server) expireDiagnostics() {
	d := &s.diagnostics
	d.mu.Lock()
	d.closed = true
	owned := d.owned
	d.owned = map[string]cd.Context{}
	d.mu.Unlock()
	for _, c := range owned {
		_, _ = s.backend.(engine.CodeDiagnosticsBackend).DetachCodeDiagnostics(context.Background(), c)
	}
	if b, ok := s.backend.(interface{ ResetCodeDiagnostics() }); ok {
		b.ResetCodeDiagnostics()
	}
}
