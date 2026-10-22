package engine

import (
	"context"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

// UnsupportedCodeDiagnostics keeps optional transports explicitly fail-closed.
// CD-09 replaces the RPC embedding with negotiated host-side operations.
type UnsupportedCodeDiagnostics struct{ Failure error }

func (u UnsupportedCodeDiagnostics) err() error {
	if u.Failure != nil {
		return u.Failure
	}
	return diagnosticError("unsupported", "Code diagnostics are not supported by this transport")
}

func (u UnsupportedCodeDiagnostics) CodeDiagnosticsHostID(context.Context) (string, error) {
	return "", u.err()
}

func (u UnsupportedCodeDiagnostics) AttachCodeDiagnostics(context.Context, string, int) (CodeDiagnosticsAttachment, error) {
	return CodeDiagnosticsAttachment{}, u.err()
}

func (u UnsupportedCodeDiagnostics) GetCodeDiagnosticsSnapshot(context.Context, cd.Context) (cd.Snapshot, error) {
	return cd.Snapshot{}, u.err()
}

func (u UnsupportedCodeDiagnostics) ApproveCodeDiagnostics(context.Context, cd.Context, string, string) (cd.Snapshot, error) {
	return cd.Snapshot{}, u.err()
}

func (u UnsupportedCodeDiagnostics) RevokeCodeDiagnostics(context.Context, cd.Context) (cd.Snapshot, error) {
	return cd.Snapshot{}, u.err()
}

func (u UnsupportedCodeDiagnostics) RunCodeDiagnostics(context.Context, cd.Context, []string) (CodeDiagnosticsRuns, error) {
	return CodeDiagnosticsRuns{}, u.err()
}

func (u UnsupportedCodeDiagnostics) CancelCodeDiagnostics(context.Context, cd.Context, []cd.RunIdentity) (CodeDiagnosticsAck, error) {
	return CodeDiagnosticsAck{}, u.err()
}

func (u UnsupportedCodeDiagnostics) DetachCodeDiagnostics(context.Context, cd.Context) (CodeDiagnosticsAck, error) {
	return CodeDiagnosticsAck{}, u.err()
}
