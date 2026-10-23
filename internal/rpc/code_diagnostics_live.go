package rpc

import (
	"context"
	"encoding/json"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

// Document text is bounded twice: decoded UTF-8 bytes (cd.MaxDocumentBytes) and
// the escaped params. Go's JSON escaping can expand one byte to six, so 5 MiB
// of text is at most 30 MiB escaped; 32 MiB of params plus the small request
// envelope stays below the existing 64 MiB app RPC frame. The frame limit is
// not raised and nothing relies on it as the only check.
const (
	maxLiveParamsBytes = 32 << 20
	maxLiveIDBytes     = 256
)

func isLiveDiagnosticsMethod(m string) bool {
	switch m {
	case "openCodeDiagnosticDocument", "changeCodeDiagnosticDocument", "saveCodeDiagnosticDocument", "closeCodeDiagnosticDocument", "navigateCode", "readNavigationFile":
		return true
	}
	return false
}

// checkLiveParams rejects oversized text by decoded and escaped size without
// including any of it in the error.
func checkLiveParams(p codeDiagnosticsParams) error {
	if len(p.Text) > cd.MaxDocumentBytes {
		return cdError("limitExceeded", "Document text exceeds the live diagnostics limit")
	}
	if len(p.Path) > 4096 || len(p.DocumentID) > maxLiveIDBytes || len(p.Revision) > maxLiveIDBytes || len(p.Op) > maxLiveIDBytes {
		return cdError("invalidArgument", "Document parameter is too large")
	}
	return nil
}

func (s *Server) dispatchLiveDiagnostics(ctx context.Context, req Request) (any, error) {
	d := &s.diagnostics
	d.mu.Lock()
	closed, host, live, navigation := d.closed, d.host, d.live, d.navigation
	d.mu.Unlock()
	switch {
	case closed:
		return nil, cdError("disconnected", "Connection closed")
	case host == "":
		return nil, cdError("unsupported", "Verified remote diagnostics identity is unavailable")
	case !live:
		// Not advertised: fail before decoding or any backend call, so a peer
		// cannot make the server parse, retain or route document text.
		return nil, cdError("unsupported", "Live code diagnostics are not supported")
	case (req.Method == "navigateCode" || req.Method == "readNavigationFile") && !navigation:
		return nil, cdError("unsupported", "Code navigation is not supported")
	case len(req.Params) > maxLiveParamsBytes:
		return nil, cdError("limitExceeded", "Document request exceeds the transport budget")
	}
	var p codeDiagnosticsParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, cdError("invalidArgument", "Invalid diagnostics parameters")
	}
	if err := checkLiveParams(p); err != nil {
		return nil, err
	}
	d.mu.Lock()
	owned := !d.closed && d.owned[p.Context.AttachmentID] == p.Context
	d.mu.Unlock()
	if !owned {
		return nil, cdError("staleContext", "Diagnostics context is not owned by this connection")
	}
	b, ok := s.backend.(engine.LiveCodeDiagnosticsBackend)
	if !ok {
		return nil, cdError("unsupported", "Live code diagnostics are not supported")
	}
	switch req.Method {
	case "navigateCode":
		nav, ok := s.backend.(engine.CodeNavigationBackend)
		if !ok {
			return nil, cdError("unsupported", "Code navigation is not supported")
		}
		if p.Position == nil || !cd.ValidNavigationOp(p.Op) {
			return nil, cdError("invalidArgument", "Invalid navigation parameters")
		}
		return nav.NavigateCode(ctx, p.Context, p.DocumentID, p.Version, p.Op, *p.Position)
	case "readNavigationFile":
		nav, ok := s.backend.(engine.CodeNavigationBackend)
		if !ok {
			return nil, cdError("unsupported", "Code navigation is not supported")
		}
		return nav.ReadNavigationFile(ctx, p.Context, p.Path)
	case "openCodeDiagnosticDocument":
		return b.OpenCodeDiagnosticDocument(ctx, p.Context, p.Path, p.DocumentID, p.Version, p.Text, p.Revision)
	case "changeCodeDiagnosticDocument":
		return b.ChangeCodeDiagnosticDocument(ctx, p.Context, p.DocumentID, p.Version, p.Text)
	case "saveCodeDiagnosticDocument":
		return b.SaveCodeDiagnosticDocument(ctx, p.Context, p.DocumentID, p.SavedVersion, p.Revision)
	default:
		return b.CloseCodeDiagnosticDocument(ctx, p.Context, p.DocumentID)
	}
}
