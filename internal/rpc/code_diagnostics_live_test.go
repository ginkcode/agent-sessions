package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

func (b *diagnosticsBackend) LiveCodeDiagnosticsReady() bool { return b.live }
func (b *diagnosticsBackend) OpenCodeDiagnosticDocument(ctx context.Context, c cd.Context, path, id string, v int64, text, rev string) (engine.CodeDiagnosticsDocument, error) {
	b.liveCalls++
	return b.m.OpenDocument(ctx, c, path, id, v, text, rev)
}

func (b *diagnosticsBackend) ChangeCodeDiagnosticDocument(ctx context.Context, c cd.Context, id string, v int64, text string) (engine.CodeDiagnosticsDocument, error) {
	b.liveCalls++
	return b.m.ChangeDocument(ctx, c, id, v, text)
}

func (b *diagnosticsBackend) SaveCodeDiagnosticDocument(ctx context.Context, c cd.Context, id string, v int64, rev string) (engine.CodeDiagnosticsDocument, error) {
	b.liveCalls++
	return b.m.SaveDocument(ctx, c, id, v, rev)
}

func (b *diagnosticsBackend) CloseCodeDiagnosticDocument(ctx context.Context, c cd.Context, id string) (engine.CodeDiagnosticsDocument, error) {
	b.liveCalls++
	return b.m.CloseDocument(ctx, c, id)
}

type rpcSession struct {
	mu    sync.Mutex
	steps []cd.DocumentStep
}

func (r *rpcSession) Session(cd.AuthorizedRoot, string) (string, cd.ServerCapabilities, error) {
	return "g", cd.ServerCapabilities{OpenClose: true, Change: 2, Save: true}, nil
}

func (r *rpcSession) OwnerSession(root cd.AuthorizedRoot, path string) (cd.DocumentOwnerIdentity, string, cd.ServerCapabilities, error) {
	gen, caps, err := r.Session(root, path)
	return cd.DocumentOwnerIdentity{ProviderID: "gopls", Policy: "fake-policy"}, gen, caps, err
}

func (r *rpcSession) Apply(_ context.Context, s cd.DocumentStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, s)
	return nil
}

func rpcCode(err error) string {
	err = normalizeDiagnosticsError(err)
	var diagnostic *cd.Error
	if errors.As(err, &diagnostic) {
		return diagnostic.Code
	}
	var rpcErr *ResponseError
	if errors.As(err, &rpcErr) && rpcErr.Data != nil {
		return rpcErr.Data.Code
	}
	return ""
}

func liveFixture(t *testing.T, live bool) (*diagnosticsBackend, *Client, cd.Context, *rpcSession, context.Context, func()) {
	t.Helper()
	b, _, c, cleanup := rpcDiagnosticsFixture(t)
	b.live = live
	sess := &rpcSession{}
	b.m.SetDocumentProvider(sess)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	init, err := c.Initialize(ctx, InitializeRequest{})
	if err != nil || !init.Capabilities.CodeDiagnostics || init.Capabilities.LiveCodeDiagnostics != live {
		t.Fatal(init, err)
	}
	a, err := c.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ApproveCodeDiagnostics(ctx, a.Context, "session", a.Snapshot.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(b.root.Project().Root, "doc.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return b, c, a.Context, sess, ctx, func() { cancel(); cleanup() }
}

func TestLiveDiagnosticsRoundTripOverRPC(t *testing.T) {
	b, c, token, sess, ctx, done := liveFixture(t, true)
	defer done()
	o, err := c.OpenCodeDiagnosticDocument(ctx, token, "doc.go", "d1", 1, "package x\r\n// \U0001F600\r\n", "r1")
	if err != nil || o.DocumentID != "d1" || o.Version != 1 || o.Context != token {
		t.Fatal(o, err)
	}
	ch, err := c.ChangeCodeDiagnosticDocument(ctx, token, "d1", 4, "package x\r\n// \U0001F600!\r\n")
	if err != nil || ch.Version != 4 {
		t.Fatal(ch, err)
	}
	if _, err = c.ChangeCodeDiagnosticDocument(ctx, token, "d1", 4, "other"); rpcCode(err) != "invalidArgument" {
		t.Fatal("conflict", err)
	}
	if _, err = c.ChangeCodeDiagnosticDocument(ctx, token, "d1", 2, "old"); rpcCode(err) != "invalidArgument" {
		t.Fatal("old version", err)
	}
	if sv, err := c.SaveCodeDiagnosticDocument(ctx, token, "d1", 4, "r2"); err != nil || sv.DocumentID != "d1" || sv.Version != 0 {
		t.Fatal(sv, err)
	}
	if _, err = c.SaveCodeDiagnosticDocument(ctx, token, "d1", 3, "r3"); rpcCode(err) != "resyncRequired" {
		t.Fatal(err)
	}
	if _, err = c.CloseCodeDiagnosticDocument(ctx, token, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CloseCodeDiagnosticDocument(ctx, token, "d1"); err != nil {
		t.Fatal("duplicate close", err)
	}
	if _, err = c.ChangeCodeDiagnosticDocument(ctx, token, "d1", 5, "x"); rpcCode(err) != "staleContext" {
		t.Fatal("closed", err)
	}
	sess.mu.Lock()
	var kinds []string
	for _, s := range sess.steps {
		kinds = append(kinds, s.Kind)
		if s.Kind == "save" && s.Text != "" {
			t.Fatal("save carried a body")
		}
	}
	sess.mu.Unlock()
	if strings.Join(kinds, ",") != "open,change,save,close" {
		t.Fatal(kinds)
	}
	// Wrong/foreign identity never reaches the backend.
	calls := b.liveCalls
	foreign := token
	foreign.ConnectionID = "other"
	var out engine.CodeDiagnosticsDocument
	if err = c.call(ctx, "openCodeDiagnosticDocument", codeDiagnosticsParams{Context: foreign, Path: "doc.go", DocumentID: "z", Version: 1}, &out); rpcCode(err) != "staleContext" || b.liveCalls != calls {
		t.Fatal("foreign context", err)
	}
	if err = c.call(ctx, "openCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, Path: "../x", DocumentID: "z", Version: 1}, &out); rpcCode(err) != "invalidArgument" && rpcCode(err) != "notFound" {
		t.Fatal("traversal", err)
	}
}

func TestLiveDiagnosticsUnsupportedFailsBeforeAnyWork(t *testing.T) {
	b, c, token, sess, ctx, done := liveFixture(t, false)
	defer done()
	calls := []func() error{
		func() error { _, e := c.OpenCodeDiagnosticDocument(ctx, token, "doc.go", "d", 1, "x", ""); return e },
		func() error { _, e := c.ChangeCodeDiagnosticDocument(ctx, token, "d", 2, "x"); return e },
		func() error { _, e := c.SaveCodeDiagnosticDocument(ctx, token, "d", 1, "r"); return e },
		func() error { _, e := c.CloseCodeDiagnosticDocument(ctx, token, "d"); return e },
	}
	for _, call := range calls {
		if e := call(); rpcCode(e) != "unsupported" {
			t.Fatal(e)
		}
	}
	// A hostile client bypassing its capability guard is refused by the server.
	var out engine.CodeDiagnosticsDocument
	for _, m := range []string{"openCodeDiagnosticDocument", "changeCodeDiagnosticDocument", "saveCodeDiagnosticDocument", "closeCodeDiagnosticDocument"} {
		if e := c.call(ctx, m, codeDiagnosticsParams{Context: token, Path: "doc.go", DocumentID: "d", Version: 1, Revision: "r", SavedVersion: 1}, &out); rpcCode(e) != "unsupported" {
			t.Fatal(m, e)
		}
	}
	if b.liveCalls != 0 || len(sess.steps) != 0 {
		t.Fatal("unsupported call reached backend/session")
	}
}

func TestLiveDiagnosticsOversizeAndOwnership(t *testing.T) {
	b, c, token, _, ctx, done := liveFixture(t, true)
	defer done()
	big := strings.Repeat("a", cd.MaxDocumentBytes+1)
	if _, e := c.OpenCodeDiagnosticDocument(ctx, token, "doc.go", "d", 1, big, ""); rpcCode(e) != "limitExceeded" {
		t.Fatal(e)
	}
	// Escaped expansion: a 5 MiB body of control characters is 30 MiB escaped and
	// still within the 32 MiB params budget; both bounds are enforced separately.
	if b.liveCalls != 0 {
		t.Fatal("oversized text reached the backend")
	}
	var out engine.CodeDiagnosticsDocument
	if e := c.call(ctx, "openCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, Path: "doc.go", DocumentID: "d", Version: 1, Text: big}, &out); rpcCode(e) != "limitExceeded" || b.liveCalls != 0 {
		t.Fatal("server accepted oversized text", e)
	}
	if e := c.call(ctx, "openCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, Path: strings.Repeat("p", 5000), DocumentID: "d", Version: 1}, &out); rpcCode(e) != "invalidArgument" {
		t.Fatal(e)
	}
	control := strings.Repeat("\x01", cd.MaxDocumentBytes)
	if _, e := c.OpenCodeDiagnosticDocument(ctx, token, "doc.go", "ctl", 1, control, ""); e != nil {
		t.Fatal("max escaped text must fit below the frame limit", e)
	}
	if _, e := c.OpenCodeDiagnosticDocument(ctx, token, "doc.go", "d2", 1, "x", ""); rpcCode(e) != "invalidArgument" {
		t.Fatal("second doc for same path", e)
	}
}
