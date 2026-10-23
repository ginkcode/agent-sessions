package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestLocalExecutableDirsAbsoluteBoundedDeduplicated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", strings.Join([]string{"/usr/bin", "relative/bin", "", "/usr/bin", "/opt/x/../y", "/opt/homebrew/bin"}, string(os.PathListSeparator)))
	t.Setenv("GOPATH", strings.Join([]string{"/gopath", "rel"}, string(os.PathListSeparator)))
	dirs := localExecutableDirs(home)
	want := []string{"/usr/bin", "/opt/homebrew/bin", filepath.Join(home, "go", "bin"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".local", "share", "mise", "installs", "go", "latest", "bin"), filepath.Join(home, ".local", "share", "mise", "installs", "node", "latest", "bin"), "/gopath/bin", "/usr/local/go/bin", "/usr/local/bin"}
	if !slices.Equal(dirs, want) {
		t.Fatalf("%v", dirs)
	}
	many := make([]string, 100)
	for i := range many {
		many[i] = "/d" + strings.Repeat("x", i+1)
	}
	t.Setenv("PATH", strings.Join(many, string(os.PathListSeparator)))
	if got := localExecutableDirs(home); len(got) != 64 {
		t.Fatal("unbounded search", len(got))
	}
}

func TestLocalGoRootFollowsResolvedExecutable(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	goroot := filepath.Join(base, "sdk", "go1")
	shims := filepath.Join(base, "shims")
	for _, d := range []string{filepath.Join(goroot, "bin"), shims, filepath.Join(base, "notexec")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(goroot, "bin", "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "notexec", "go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(goroot, "bin", "go"), filepath.Join(shims, "go")); err != nil {
		t.Fatal(err)
	}
	if got := localGoRoot([]string{filepath.Join(base, "missing"), filepath.Join(base, "notexec"), shims}); got != goroot {
		t.Fatalf("%q", got)
	}
	if got := localGoRoot([]string{filepath.Join(base, "notexec")}); got != "" {
		t.Fatal("non-executable go accepted", got)
	}
}

func TestDiscoverLocalLiveMissingToolsIsStatusNotError(t *testing.T) {
	if !localLiveSupported() {
		t.Skip("local live platform gate")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/a\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := cd.AuthorizeRoot("local:test", dir, func(p string) bool { return p == dir })
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOPATH", "")
	// Conventional system directories may hold real tools on a developer host;
	// only assert when none of them provides gopls.
	for _, d := range localExecutableDirs(home) {
		if _, err := os.Stat(filepath.Join(d, "gopls")); err == nil {
			t.Skip("host has gopls in a conventional directory")
		}
	}
	regs, err := discoverLocalLive(root)
	if err != nil || len(regs) != 3 || regs[0].ProviderID != "gopls" || regs[0].Availability != "missingTool" {
		t.Fatal(err, regs)
	}
	for _, r := range regs {
		if r.Availability == "available" {
			t.Fatal("unavailable host offered a server", r.ProviderID)
		}
	}
}

func TestLocalLiveOptionOnlyThroughExplicitOption(t *testing.T) {
	e := NewEngine(WithCacheEnabled(false))
	defer func() { _ = e.Close() }()
	if e.LiveCodeDiagnosticsReady() {
		t.Fatal("default Engine advertised live")
	}
	l := NewEngine(WithCacheEnabled(false), WithLocalLiveCodeDiagnostics())
	defer func() { _ = l.Close() }()
	if l.LiveCodeDiagnosticsReady() != localLiveSupported() {
		t.Fatal("local option readiness must follow the platform gate")
	}
}

// Real installed-gopls smoke on a disposable synthetic module through the
// production local route: discoverLocalLive (metadata only) + HostSessionLaunch,
// composite approval, an unsaved draft with a syntax error, then the fixed text
// and its empty publication. Nothing is saved, installed or downloaded:
// GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOWORK=off GOENV=off are fixed in
// the adapter environment. Skips when no supported gopls is installed.
func TestLocalLiveRealGoplsSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("real language server")
	}
	if !localLiveSupported() {
		t.Skip("local live platform gate")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"go.mod": "module example.test/smoke\n\ngo 1.21\n", "a.go": "package smoke\n\nfunc A() int { return 1 }\n"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := cd.AuthorizeRoot("local:smoke", dir, func(p string) bool { return p == dir })
	if err != nil {
		t.Fatal(err)
	}
	regs, err := discoverLocalLive(root)
	if err != nil {
		t.Fatal(err)
	}
	if regs[0].Availability != "available" {
		t.Skipf("no supported gopls installed: %s (%s)", regs[0].Availability, regs[0].Live.State.Notice)
	}
	t.Logf("gopls executable %s", regs[0].Live.Server.Executable)
	for _, tool := range regs[0].Live.Server.Tools() {
		t.Logf("approval input %s %s", tool.ProviderID, tool.Executable)
	}
	if goBin := regs[0].Live.Server.Toolchain[0].Executable; goBin != "" {
		if out, err := exec.Command(goBin, "version").Output(); err == nil {
			t.Logf("toolchain %s", strings.TrimSpace(string(out)))
		}
	}

	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "smoke"}, CWD: dir, RepoRoot: dir}})
	var mu sync.Mutex
	var events []cd.Event
	emit := EmitterFunc(func(name string, payload any) {
		if ev, ok := payload.(cd.Event); ok && name == CodeDiagnosticsUpdatedEvent {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	})
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir()}), WithEmitter(emit), WithLocalLiveCodeDiagnostics())
	t.Cleanup(func() { _ = e.Close() })
	if !e.LiveCodeDiagnosticsReady() {
		t.Fatal("local route not ready")
	}
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
	broken := "package smoke\n\nfunc A() int { return 1 \n"
	if _, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.go", "draft", 1, broken, ""); err == nil {
		t.Fatal("opened before approval")
	} else {
		requireDiagnosticCode(t, err, "approvalRequired")
	}
	if _, err = e.ApproveCodeDiagnostics(ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.go", "draft", 1, broken, ""); err != nil {
		t.Fatal(err)
	}
	gopls := func(s cd.Snapshot) *cd.ResultSet {
		for i := range s.ResultSets {
			if s.ResultSets[i].ProviderID == "gopls" && s.ResultSets[i].Scope.Path == "a.go" {
				return &s.ResultSets[i]
			}
		}
		return nil
	}
	waitLocal(t, 60*time.Second, "syntax diagnostic", func() bool {
		s, err = e.GetCodeDiagnosticsSnapshot(ctx, c)
		r := gopls(s)
		return err == nil && r != nil && len(r.Diagnostics) > 0 && r.Diagnostics[0].Basis.Version != nil && *r.Diagnostics[0].Basis.Version == 1
	})
	d := gopls(s).Diagnostics[0]
	t.Logf("draft v1 diagnostic after %s: %q severity=%s freshness=%s range=%+v", time.Since(start).Round(time.Millisecond), d.Message, d.Severity, d.Freshness, d.Range)
	if d.Freshness != "current" || d.Range == nil || d.Basis.DocumentID != "draft" {
		t.Fatalf("%+v", d)
	}
	waitLocal(t, 10*time.Second, "updated event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range events {
			if r := gopls(ev.Snapshot); ev.Context == c && r != nil && len(r.Diagnostics) > 0 {
				return true
			}
		}
		return false
	})
	if _, err = e.ChangeCodeDiagnosticDocument(ctx, c, "draft", 2, files["a.go"]); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	waitLocal(t, 60*time.Second, "empty clear", func() bool {
		s, err = e.GetCodeDiagnosticsSnapshot(ctx, c)
		r := gopls(s)
		return err == nil && (r == nil || len(r.Diagnostics) == 0)
	})
	t.Logf("draft v2 cleared after %s", time.Since(start).Round(time.Millisecond))
	if _, err = e.CloseCodeDiagnosticDocument(ctx, c, "draft"); err != nil {
		t.Fatal(err)
	}
	// Drafts are never saved: the file on disk is unchanged.
	if data, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(data) != files["a.go"] {
		t.Fatal("draft written to disk")
	}
}

func waitLocal(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}
