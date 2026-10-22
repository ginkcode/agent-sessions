package app

import (
	"context"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

func (u offlineBackend) CodeDiagnosticsHostID(context.Context) (string, error) {
	return "", &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) AttachCodeDiagnostics(context.Context, string, int) (engine.CodeDiagnosticsAttachment, error) {
	return engine.CodeDiagnosticsAttachment{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) GetCodeDiagnosticsSnapshot(context.Context, cd.Context) (cd.Snapshot, error) {
	return cd.Snapshot{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) ApproveCodeDiagnostics(context.Context, cd.Context, string, string) (cd.Snapshot, error) {
	return cd.Snapshot{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) RevokeCodeDiagnostics(context.Context, cd.Context) (cd.Snapshot, error) {
	return cd.Snapshot{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) RunCodeDiagnostics(context.Context, cd.Context, []string) (engine.CodeDiagnosticsRuns, error) {
	return engine.CodeDiagnosticsRuns{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) CancelCodeDiagnostics(context.Context, cd.Context, []cd.RunIdentity) (engine.CodeDiagnosticsAck, error) {
	return engine.CodeDiagnosticsAck{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) DetachCodeDiagnostics(context.Context, cd.Context) (engine.CodeDiagnosticsAck, error) {
	return engine.CodeDiagnosticsAck{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) OpenCodeDiagnosticDocument(context.Context, cd.Context, string, string, int64, string, string) (engine.CodeDiagnosticsDocument, error) {
	return engine.CodeDiagnosticsDocument{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) ChangeCodeDiagnosticDocument(context.Context, cd.Context, string, int64, string) (engine.CodeDiagnosticsDocument, error) {
	return engine.CodeDiagnosticsDocument{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) SaveCodeDiagnosticDocument(context.Context, cd.Context, string, int64, string) (engine.CodeDiagnosticsDocument, error) {
	return engine.CodeDiagnosticsDocument{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}

func (u offlineBackend) CloseCodeDiagnosticDocument(context.Context, cd.Context, string) (engine.CodeDiagnosticsDocument, error) {
	return engine.CodeDiagnosticsDocument{}, &cd.Error{Code: "disconnected", Message: "Selected host is disconnected"}
}
