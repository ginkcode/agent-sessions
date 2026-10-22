package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func requireDiagnosticCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *cd.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestCodeDiagnosticsAuthorizationLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "diagnostics"}, CWD: nested, RepoRoot: dir}})
	svc := NewService(catalog, nil)
	// Inject a verified host owner so lifecycle/security tests are platform-neutral
	// and never use the developer's grant database or installed executables.
	owner, err := newDiagnosticsOwner(t.TempDir(), "test-host")
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(WithService(svc), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() { _ = e.Close() })
	_, err = e.AttachCodeDiagnostics(ctx, filepath.Join(dir, "unknown"), 1)
	requireDiagnosticCode(t, err, "unauthorized")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = e.AttachCodeDiagnostics(canceled, nested, 1)
	requireDiagnosticCode(t, err, "canceled")
	attached, err := e.AttachCodeDiagnostics(ctx, nested, 1)
	if err != nil {
		t.Fatal(err)
	}
	realNested, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Project.Root != realNested {
		t.Fatalf("promoted nested root: %s", attached.Project.Root)
	}
	c := attached.Context
	_, err = e.RunCodeDiagnostics(ctx, c, nil)
	if err == nil {
		t.Fatal("run without approval succeeded")
	}
	_, err = e.ApproveCodeDiagnostics(ctx, c, "session", "unknown")
	requireDiagnosticCode(t, err, "approvalChanged")
	foreign := c
	foreign.ConnectionID = "foreign"
	_, err = e.GetCodeDiagnosticsSnapshot(ctx, foreign)
	requireDiagnosticCode(t, err, "staleContext")
	// Allow metadata completion to settle before revoking; neither stage executes.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := e.GetCodeDiagnosticsSnapshot(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Providers) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("discovery did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	revoked, err := e.RevokeCodeDiagnostics(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Context.Generation == c.Generation || revoked.Approval.State != "revoked" || len(revoked.ResultSets) != 0 {
		t.Fatal("revoke did not invalidate epoch")
	}
	_, err = e.GetCodeDiagnosticsSnapshot(ctx, c)
	requireDiagnosticCode(t, err, "staleContext")
	_, err = e.RunCodeDiagnostics(ctx, revoked.Context, nil)
	if err == nil {
		t.Fatal("revoked run succeeded")
	}
	_, err = e.DetachCodeDiagnostics(ctx, revoked.Context)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.GetCodeDiagnosticsSnapshot(ctx, revoked.Context)
	requireDiagnosticCode(t, err, "staleContext")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = e.AttachCodeDiagnostics(ctx, dir, 1)
	requireDiagnosticCode(t, err, "disconnected")
}

func TestCodeDiagnosticsCatalogFirst(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.AuthorizeCodeDiagnosticsRoot(context.Background(), "host", filepath.Join(t.TempDir(), "does-not-exist"))
	requireDiagnosticCode(t, err, "unauthorized")
}

func TestCodeDiagnosticsDocumentsUnsupportedWithoutAdapters(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "documents"}, CWD: dir, RepoRoot: dir}})
	owner, err := newDiagnosticsOwner(t.TempDir(), "test-host")
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() { _ = e.Close() })
	attached, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := attached.Context
	// No concrete adapter installs a provider: every operation is unsupported and
	// starts nothing, with or without approval, and creates no document state.
	_, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.go", "d", 1, "package a", "")
	requireDiagnosticCode(t, err, "unsupported")
	_, err = e.ChangeCodeDiagnosticDocument(ctx, c, "d", 2, "x")
	requireDiagnosticCode(t, err, "unsupported")
	_, err = e.SaveCodeDiagnosticDocument(ctx, c, "d", 1, "r")
	requireDiagnosticCode(t, err, "unsupported")
	if _, err = e.CloseCodeDiagnosticDocument(ctx, c, "d"); err == nil {
		t.Fatal("close of an unknown document succeeded")
	}
	foreign := c
	foreign.AttachmentID = "other"
	_, err = e.OpenCodeDiagnosticDocument(ctx, foreign, "a.go", "d", 1, "x", "")
	requireDiagnosticCode(t, err, "staleContext")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = e.OpenCodeDiagnosticDocument(canceled, c, "a.go", "d", 1, "x", "")
	requireDiagnosticCode(t, err, "canceled")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.go", "d", 1, "x", "")
	requireDiagnosticCode(t, err, "disconnected")
}

func TestInvalidateCodeDiagnosticsPathContextChecks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "inv"}, CWD: dir, RepoRoot: dir}})
	owner, err := newDiagnosticsOwner(t.TempDir(), "test-host")
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() { _ = e.Close() })
	a, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.InvalidateCodeDiagnosticsPath(ctx, a.Context, "x.go"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.InvalidateCodeDiagnosticsPath(ctx, a.Context, "../x.go"); err == nil {
		t.Fatal("escape accepted")
	}
	foreign := a.Context
	foreign.AttachmentID = "other"
	if _, err = e.InvalidateCodeDiagnosticsPath(ctx, foreign, "x.go"); err == nil {
		t.Fatal("foreign attachment accepted")
	}
	old := a.Context
	old.Generation = "old"
	_, err = e.InvalidateCodeDiagnosticsPath(ctx, old, "x.go")
	requireDiagnosticCode(t, err, "staleContext")
	catalog.Reset(nil)
	if _, err = e.InvalidateCodeDiagnosticsPath(ctx, a.Context, "x.go"); err == nil {
		t.Fatal("removed catalog root accepted")
	}
}
