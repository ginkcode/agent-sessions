package app

import (
	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

// liveRoute applies the same expected-host, offline and connection-epoch guards
// as Stage 1, then requires the separate live surface. A backend without it
// (an old server) fails with unsupported before anything is dispatched.
func (a *App) liveRoute(expectedHostID string) (engine.LiveCodeDiagnosticsBackend, uint64, error) {
	b, gen, err := a.diagnosticsRoute(expectedHostID)
	if err != nil {
		return nil, 0, err
	}
	live, ok := b.(engine.LiveCodeDiagnosticsBackend)
	if !ok {
		return nil, 0, &cd.Error{Code: "unsupported", Message: "Live code diagnostics are unavailable on this backend"}
	}
	return live, gen, nil
}

func (a *App) liveResult(gen uint64, out engine.CodeDiagnosticsDocument, err error) (engine.CodeDiagnosticsDocument, error) {
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return engine.CodeDiagnosticsDocument{}, stale
	}
	return out, err
}

func (a *App) OpenCodeDiagnosticDocument(c cd.Context, path, documentID string, version int64, text, revision, expectedHostID string) (engine.CodeDiagnosticsDocument, error) {
	b, gen, err := a.liveRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsDocument{}, err
	}
	out, err := b.OpenCodeDiagnosticDocument(a.appCtx(), c, path, documentID, version, text, revision)
	return a.liveResult(gen, out, err)
}

func (a *App) ChangeCodeDiagnosticDocument(c cd.Context, documentID string, version int64, text, expectedHostID string) (engine.CodeDiagnosticsDocument, error) {
	b, gen, err := a.liveRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsDocument{}, err
	}
	out, err := b.ChangeCodeDiagnosticDocument(a.appCtx(), c, documentID, version, text)
	return a.liveResult(gen, out, err)
}

func (a *App) SaveCodeDiagnosticDocument(c cd.Context, documentID string, savedVersion int64, revision, expectedHostID string) (engine.CodeDiagnosticsDocument, error) {
	b, gen, err := a.liveRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsDocument{}, err
	}
	out, err := b.SaveCodeDiagnosticDocument(a.appCtx(), c, documentID, savedVersion, revision)
	return a.liveResult(gen, out, err)
}

func (a *App) CloseCodeDiagnosticDocument(c cd.Context, documentID, expectedHostID string) (engine.CodeDiagnosticsDocument, error) {
	b, gen, err := a.liveRoute(expectedHostID)
	if err != nil {
		return engine.CodeDiagnosticsDocument{}, err
	}
	out, err := b.CloseCodeDiagnosticDocument(a.appCtx(), c, documentID)
	return a.liveResult(gen, out, err)
}
