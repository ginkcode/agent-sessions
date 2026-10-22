package engine

import (
	"context"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
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

var _ LiveCodeDiagnosticsBackend = (*Engine)(nil)
