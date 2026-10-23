package engine

import (
	"context"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/files"
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

func (u UnsupportedCodeDiagnostics) OpenCodeDiagnosticDocument(context.Context, cd.Context, string, string, int64, string, string) (CodeDiagnosticsDocument, error) {
	return CodeDiagnosticsDocument{}, u.err()
}

func (u UnsupportedCodeDiagnostics) ChangeCodeDiagnosticDocument(context.Context, cd.Context, string, int64, string) (CodeDiagnosticsDocument, error) {
	return CodeDiagnosticsDocument{}, u.err()
}

func (u UnsupportedCodeDiagnostics) SaveCodeDiagnosticDocument(context.Context, cd.Context, string, int64, string) (CodeDiagnosticsDocument, error) {
	return CodeDiagnosticsDocument{}, u.err()
}

func (u UnsupportedCodeDiagnostics) CloseCodeDiagnosticDocument(context.Context, cd.Context, string) (CodeDiagnosticsDocument, error) {
	return CodeDiagnosticsDocument{}, u.err()
}

func (u UnsupportedCodeDiagnostics) NavigateCode(context.Context, cd.Context, string, int64, string, cd.Position) (cd.NavigationResult, error) {
	return cd.NavigationResult{}, u.err()
}

func (u UnsupportedCodeDiagnostics) ReadNavigationFile(context.Context, cd.Context, string) (files.Content, error) {
	return files.Content{}, u.err()
}
