package app

import (
	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
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

// NavigateCode asks the language server that holds a live document about the
// symbol at position: op is definition, references or hover. It goes over the
// same route as the document operations.
func (a *App) NavigateCode(c cd.Context, documentID string, version int64, op string, position cd.Position, expectedHostID string) (cd.NavigationResult, error) {
	b, gen, err := a.liveRoute(expectedHostID)
	if err != nil {
		return cd.NavigationResult{}, err
	}
	nav, ok := b.(engine.CodeNavigationBackend)
	if !ok {
		return cd.NavigationResult{}, &cd.Error{Code: "unsupported", Message: "Code navigation is unavailable on this backend"}
	}
	out, err := nav.NavigateCode(a.appCtx(), c, documentID, version, op, position)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return cd.NavigationResult{}, stale
	}
	return out, err
}

// ReadNavigationFile reads, for viewing only, an external file (absolute host
// path) that a navigation answer of the same attachment pointed to.
func (a *App) ReadNavigationFile(c cd.Context, path, expectedHostID string) (files.Content, error) {
	b, gen, err := a.liveRoute(expectedHostID)
	if err != nil {
		return files.Content{}, err
	}
	nav, ok := b.(engine.CodeNavigationBackend)
	if !ok {
		return files.Content{}, &cd.Error{Code: "unsupported", Message: "Code navigation is unavailable on this backend"}
	}
	out, err := nav.ReadNavigationFile(a.appCtx(), c, path)
	if stale := a.diagnosticsCurrent(gen); stale != nil {
		return files.Content{}, stale
	}
	return out, err
}
