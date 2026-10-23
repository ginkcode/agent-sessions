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

// Real vtsls smoke on a disposable synthetic package through the production
// adapter path: DiscoverLocalLive (metadata only) + HostSessionLaunch, composite
// approval, unsaved .ts and .mjs drafts with a syntax error, then the fixed
// text and its empty publication. The install is supplied explicitly, never
// searched for: AGENT_SESSIONS_VTSLS_DIR names a directory holding a vtsls
// link (for example an approved existing install's node_modules/.bin) and
// AGENT_SESSIONS_VTSLS_NODE_DIR the directory holding node. HOME is a temp
// directory, so the absence of its TypeScript typings cache shows automatic
// type acquisition stayed off. Nothing is installed, downloaded or saved.
func TestLocalLiveRealVtslsSmoke(t *testing.T) {
	vtslsDir, nodeDir := os.Getenv("AGENT_SESSIONS_VTSLS_DIR"), os.Getenv("AGENT_SESSIONS_VTSLS_NODE_DIR")
	if testing.Short() || vtslsDir == "" || nodeDir == "" {
		t.Skip("real language server: set AGENT_SESSIONS_VTSLS_DIR and AGENT_SESSIONS_VTSLS_NODE_DIR")
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
		"package.json":  `{"name":"smoke","private":true,"type":"module"}` + "\n",
		"tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true,"target":"es2022","module":"nodenext"}}` + "\n",
		"a.ts":          "export const a: number = 1;\n",
		"b.mjs":         "export const b = 1;\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		return cd.DiscoverLocalLive(root, cd.LocalLiveEnvironment{
			Path: []string{vtslsDir},
			Go:   cd.GoplsEnvironment{Home: home, Cache: home, ModuleCache: home},
			TS:   cd.TSEnvironment{Home: home, NodeDir: nodeDir},
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
	ts := regs[1]
	if ts.ProviderID != "typescript" || ts.Availability != "available" {
		t.Fatalf("vtsls not available: %s (%s)", ts.Availability, ts.Live.State.Notice)
	}
	t.Logf("launch %s %v", ts.Live.Server.Executable, ts.Live.Server.Args)
	for _, tool := range ts.Live.Server.Tools() {
		t.Logf("approval input %s %s", tool.ProviderID, tool.Executable)
	}

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
	if _, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.ts", "ts-draft", 1, "export const a: number = ;\n", ""); err == nil {
		t.Fatal("opened before approval")
	} else {
		requireDiagnosticCode(t, err, "approvalRequired")
	}
	if _, err = e.ApproveCodeDiagnostics(ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	result := func(s cd.Snapshot, path string) *cd.ResultSet {
		for i := range s.ResultSets {
			if s.ResultSets[i].ProviderID == "typescript" && s.ResultSets[i].Scope.Path == path {
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
	for _, doc := range []struct{ path, id, broken string }{
		{"a.ts", "ts-draft", "export const a: number = ;\n"},
		{"b.mjs", "mjs-draft", "export const b = ;\n"},
	} {
		start := time.Now()
		if _, err = e.OpenCodeDiagnosticDocument(ctx, c, doc.path, doc.id, 1, doc.broken, ""); err != nil {
			t.Fatal(err)
		}
		wait(90*time.Second, doc.path+" syntax diagnostic", func(s cd.Snapshot) bool {
			r := result(s, doc.path)
			return r != nil && len(r.Diagnostics) > 0
		})
		d := result(s, doc.path).Diagnostics[0]
		t.Logf("%s draft diagnostic after %s: %q code=%s source=%s severity=%s freshness=%s", doc.path, time.Since(start).Round(time.Millisecond), d.Message, d.Code, d.Source, d.Severity, d.Freshness)
		// vtsls pushes are unversioned: listed as uncertain, never positioned inline.
		if d.Freshness != "uncertain" || d.Range != nil || d.Basis.Version != nil {
			t.Fatalf("%+v", d)
		}
		if _, err = e.ChangeCodeDiagnosticDocument(ctx, c, doc.id, 2, files[doc.path]); err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		wait(60*time.Second, doc.path+" empty clear", func(s cd.Snapshot) bool {
			r := result(s, doc.path)
			return r == nil || len(r.Diagnostics) == 0
		})
		t.Logf("%s fixed draft cleared after %s", doc.path, time.Since(start).Round(time.Millisecond))
		if _, err = e.CloseCodeDiagnosticDocument(ctx, c, doc.id); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != body {
			t.Fatal("draft written to disk", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "Caches", "typescript")); !os.IsNotExist(err) {
		t.Fatal("automatic type acquisition wrote its typings cache", err)
	}
	entries, _ := os.ReadDir(home)
	for _, entry := range entries {
		t.Logf("HOME entry after run: %s", entry.Name())
	}
}
