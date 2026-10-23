package engine

import (
	"context"
	"errors"
	"os"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/files"
)

// LiveCodeDiagnosticsBackend is the separate Stage 2 surface (capability
// liveCodeDiagnostics). Implementing CodeDiagnosticsBackend never implies it.
// Text is the actual displayed document; it is never logged or persisted.
type LiveCodeDiagnosticsBackend interface {
	OpenCodeDiagnosticDocument(ctx context.Context, c cd.Context, path, documentID string, version int64, text, revision string) (CodeDiagnosticsDocument, error)
	ChangeCodeDiagnosticDocument(ctx context.Context, c cd.Context, documentID string, version int64, text string) (CodeDiagnosticsDocument, error)
	SaveCodeDiagnosticDocument(ctx context.Context, c cd.Context, documentID string, savedVersion int64, revision string) (CodeDiagnosticsDocument, error)
	CloseCodeDiagnosticDocument(ctx context.Context, c cd.Context, documentID string) (CodeDiagnosticsDocument, error)
}

// CodeDiagnosticsDocument is the identity-only acknowledgement of a document
// operation: {context, documentId, version?}.
type CodeDiagnosticsDocument = cd.DocumentReceipt

// SetCodeDiagnosticsDocumentProvider installs the host-local language session
// seam (concrete adapters, CD-22…CD-24). Until one is installed every document
// operation fails with `unsupported` before any process can start.
func (e *Engine) SetCodeDiagnosticsDocumentProvider(ctx context.Context, p cd.DocumentProvider) error {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return err
	}
	o.manager.SetDocumentProvider(p)
	return nil
}

func (e *Engine) OpenCodeDiagnosticDocument(ctx context.Context, c cd.Context, path, documentID string, version int64, text, revision string) (CodeDiagnosticsDocument, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsDocument{}, err
	}
	return o.manager.OpenDocument(ctx, c, path, documentID, version, text, revision)
}

func (e *Engine) ChangeCodeDiagnosticDocument(ctx context.Context, c cd.Context, documentID string, version int64, text string) (CodeDiagnosticsDocument, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsDocument{}, err
	}
	return o.manager.ChangeDocument(ctx, c, documentID, version, text)
}

func (e *Engine) SaveCodeDiagnosticDocument(ctx context.Context, c cd.Context, documentID string, savedVersion int64, revision string) (CodeDiagnosticsDocument, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsDocument{}, err
	}
	return o.manager.SaveDocument(ctx, c, documentID, savedVersion, revision)
}

func (e *Engine) CloseCodeDiagnosticDocument(ctx context.Context, c cd.Context, documentID string) (CodeDiagnosticsDocument, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return CodeDiagnosticsDocument{}, err
	}
	return o.manager.CloseDocument(ctx, c, documentID)
}

// CodeNavigationBackend answers definition, references and hover for a live
// document (capability codeNavigation). It needs the live surface: only an
// open document at its current version can be asked about.
// ReadNavigationFile reads, for viewing only, an external file a navigation
// answer of the same attachment pointed to (a dependency's source).
type CodeNavigationBackend interface {
	NavigateCode(ctx context.Context, c cd.Context, documentID string, version int64, op string, pos cd.Position) (cd.NavigationResult, error)
	ReadNavigationFile(ctx context.Context, c cd.Context, path string) (files.Content, error)
}

func (e *Engine) NavigateCode(ctx context.Context, c cd.Context, documentID string, version int64, op string, pos cd.Position) (cd.NavigationResult, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return cd.NavigationResult{}, err
	}
	return o.manager.Navigate(ctx, c, documentID, version, op, pos)
}

func (e *Engine) ReadNavigationFile(ctx context.Context, c cd.Context, path string) (files.Content, error) {
	o, err := e.diagnostics(ctx)
	if err != nil {
		return files.Content{}, err
	}
	if err = o.manager.NavigationGrant(c, path); err != nil {
		return files.Content{}, err
	}
	out, err := files.ReadExternal(e.editPolicy(), path)
	switch {
	case err == nil:
		return out, nil
	case errors.Is(err, files.ErrEditProtected):
		return files.Content{}, &cd.Error{Code: "unsupported", Message: "This file holds credentials or agent data and is not shown"}
	case errors.Is(err, os.ErrNotExist):
		return files.Content{}, &cd.Error{Code: "notFound", Message: "The file no longer exists"}
	default:
		return files.Content{}, &cd.Error{Code: "toolFailed", Message: "The file could not be read: " + err.Error()}
	}
}

var (
	_ LiveCodeDiagnosticsBackend = (*Engine)(nil)
	_ CodeNavigationBackend      = (*Engine)(nil)
)
