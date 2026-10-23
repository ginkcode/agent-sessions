package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// Real Ruff smoke on a disposable synthetic project through the production
// adapter path: DiscoverLocalLive (wheel metadata only) + HostSessionLaunch,
// composite approval, an unsaved draft with a syntax error and an unused
// import, then the fixed text and its empty publication. The install is
// supplied explicitly, never searched for: AGENT_SESSIONS_RUFF_DIR names a
// directory holding a ruff link from a uv tool, pipx or pip install (for
// example ~/.local/bin). HOME is an empty temp directory so no user Ruff
// configuration applies. Nothing is installed, downloaded or saved.
func TestLocalLiveRealRuffSmoke(t *testing.T) {
	ruffDir := os.Getenv("AGENT_SESSIONS_RUFF_DIR")
	if testing.Short() || ruffDir == "" {
		t.Skip("real language server: set AGENT_SESSIONS_RUFF_DIR")
	}
	if !localLiveSupported() {
		t.Skip("local live platform gate")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	files := map[string]string{
		"pyproject.toml": "[project]\nname = \"smoke\"\nversion = \"0\"\n\n[tool.ruff.lint]\nselect = [\"E\", \"F\"]\n",
		"a.py":           "x = 1\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		return cd.DiscoverLocalLive(root, cd.LocalLiveEnvironment{
			Path: []string{ruffDir},
			Go:   cd.GoplsEnvironment{Home: home, Cache: home, ModuleCache: home},
			TS:   cd.TSEnvironment{Home: home},
			Ruff: cd.RuffEnvironment{Home: home},
		})
	}
	root, err := cd.AuthorizeRoot("local:smoke", dir, func(p string) bool { return p == dir })
	if err != nil {
		t.Fatal(err)
	}
	regs, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	ruff := regs[3]
	if ruff.ProviderID != "ruff" || ruff.Availability != "available" {
		t.Fatalf("ruff not available: %s (%s)", ruff.Availability, ruff.Live.State.Notice)
	}
	t.Logf("launch %s %v", ruff.Live.Server.Executable, ruff.Live.Server.Args)

	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "smoke"}, CWD: dir, RepoRoot: dir}})
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir()}),
		WithInjectedLiveCodeDiagnostics(LiveCodeDiagnosticsInjection{Discover: discover, Session: cd.SessionOptions{Launch: cd.HostSessionLaunch}}))
	t.Cleanup(func() { _ = e.Close() })
	ctx := context.Background()
	att, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := att.Context
	var s cd.Snapshot
	waitLocal(t, 30*time.Second, "proposal", func() bool {
		s, err = e.GetCodeDiagnosticsSnapshot(ctx, c)
		return err == nil && s.Proposal != nil
	})
	if _, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.py", "py-draft", 1, "x = (\n", ""); err == nil {
		t.Fatal("opened before approval")
	} else {
		requireDiagnosticCode(t, err, "approvalRequired")
	}
	if _, err = e.ApproveCodeDiagnostics(ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	result := func(s cd.Snapshot, path string) *cd.ResultSet {
		for i := range s.ResultSets {
			if s.ResultSets[i].ProviderID == "ruff" && s.ResultSets[i].Scope.Path == path {
				return &s.ResultSets[i]
			}
		}
		return nil
	}
	// wait reports the provider state on timeout so a real-server failure is legible.
	wait := func(limit time.Duration, what string, cond func(cd.Snapshot) bool) {
		t.Helper()
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			if s, err = e.GetCodeDiagnosticsSnapshot(ctx, c); err == nil && cond(s) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		for _, p := range s.Providers {
			t.Logf("provider %s %s phase=%s freshness=%s notice=%q failure=%+v", p.ProviderID, p.Availability, p.Phase, p.Freshness, p.Notice, p.Failure)
		}
		t.Logf("result sets %+v", s.ResultSets)
		t.Fatalf("timed out: %s (%v)", what, err)
	}
	// Ruff pushes carry the document version: current and positioned inline.
	// Ruff reports only the syntax error while the file does not parse, then
	// lint findings for the next draft.
	only := func(version int64, code, severity string) {
		t.Helper()
		start := time.Now()
		wait(30*time.Second, code, func(s cd.Snapshot) bool {
			r := result(s, "a.py")
			return r != nil && len(r.Diagnostics) > 0 && r.Diagnostics[0].Basis.Version != nil && *r.Diagnostics[0].Basis.Version == version
		})
		ds := result(s, "a.py").Diagnostics
		d := ds[0]
		t.Logf("v%d after %s: %q code=%s source=%s severity=%s freshness=%s", version, time.Since(start).Round(time.Millisecond), d.Message, d.Code, d.Source, d.Severity, d.Freshness)
		if len(ds) != 1 || d.Code != code || d.Severity != severity || d.Source != "ruff/Ruff" || d.Freshness != "current" || d.Range == nil {
			t.Fatalf("%+v", ds)
		}
	}
	if _, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.py", "py-draft", 1, "x = (\n", ""); err != nil {
		t.Fatal(err)
	}
	only(1, "invalid-syntax", "error")
	if _, err = e.ChangeCodeDiagnosticDocument(ctx, c, "py-draft", 2, "import os\nx = 1\n"); err != nil {
		t.Fatal(err)
	}
	only(2, "F401", "warning")
	if _, err = e.ChangeCodeDiagnosticDocument(ctx, c, "py-draft", 3, files["a.py"]); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	wait(30*time.Second, "empty clear", func(s cd.Snapshot) bool {
		r := result(s, "a.py")
		return r == nil || len(r.Diagnostics) == 0
	})
	t.Logf("fixed draft cleared after %s", time.Since(start).Round(time.Millisecond))
	if _, err = e.CloseCodeDiagnosticDocument(ctx, c, "py-draft"); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != body {
			t.Fatal("draft written to disk", name)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("Ruff wrote to HOME: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ruff_cache")); !os.IsNotExist(err) {
		t.Fatal("Ruff wrote a cache into the project", err)
	}
}
