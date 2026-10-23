package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// The approved project watcher starts only after approval, publishes unopened
// saved-file syntax errors, follows real fsnotify changes and is joined on
// revoke, detach and owner close. Nothing executes: approval only records a
// grant; go-syntax parses in process.
func TestCodeDiagnosticsProjectWatcherLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/w\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package w\nfunc {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "watch"}, CWD: dir, RepoRoot: dir}})
	owner, err := newDiagnosticsOwner(t.TempDir(), "test-host")
	if err != nil {
		t.Fatal(err)
	}
	owner.watch = cd.ProjectWatchOptions{Debounce: 20 * time.Millisecond}
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() { _ = e.Close() })
	attached, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := attached.Context
	snapshot := func(c cd.Context) cd.Snapshot {
		s, err := e.GetCodeDiagnosticsSnapshot(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	await := func(what string, c cd.Context, ok func(cd.Snapshot) bool) cd.Snapshot {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if s := snapshot(c); ok(s) {
				return s
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%s: %+v", what, snapshot(c))
		return cd.Snapshot{}
	}
	syntax := func(s cd.Snapshot) (cd.ProviderState, []cd.Diagnostic) {
		var p cd.ProviderState
		for _, q := range s.Providers {
			if q.ProviderID == cd.GoSyntaxProviderID {
				p = q
			}
		}
		var out []cd.Diagnostic
		for _, set := range s.ResultSets {
			if set.ProviderID == cd.GoSyntaxProviderID {
				out = set.Diagnostics
			}
		}
		return p, out
	}
	s := await("discovery", c, func(s cd.Snapshot) bool { p, _ := syntax(s); return p.ProviderID != "" })
	if s.Proposal == nil {
		t.Fatal("go sources present but no approval proposal")
	}
	time.Sleep(50 * time.Millisecond)
	if _, found := syntax(snapshot(c)); found != nil || owner.projectWatchers() != 0 {
		t.Fatal("watched or analyzed before approval")
	}
	if _, err = e.ApproveCodeDiagnostics(ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	await("unopened syntax error", c, func(s cd.Snapshot) bool {
		p, d := syntax(s)
		return p.Coverage.ExternalChanges == "watching" && len(d) > 0 && d[0].Path == "a.go"
	})
	if owner.projectWatchers() != 1 {
		t.Fatal("watcher not owned")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package w\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	await("fix", c, func(s cd.Snapshot) bool { _, d := syntax(s); return d != nil && len(d) == 0 })
	revoked, err := e.RevokeCodeDiagnostics(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if owner.projectWatchers() != 0 {
		t.Fatal("revoke left the watcher running")
	}
	if p, d := syntax(revoked); d != nil || p.Coverage.ExternalChanges != "unwatched" {
		t.Fatalf("revoked state %+v", p)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("func {"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, d := syntax(snapshot(revoked.Context)); d != nil {
		t.Fatal("published after revoke")
	}
	// Reapproval restarts under the new generation; detach joins it.
	if _, err = e.ApproveCodeDiagnostics(ctx, revoked.Context, "session", revoked.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	await("reapproved", revoked.Context, func(s cd.Snapshot) bool { _, d := syntax(s); return len(d) > 0 })
	if _, err = e.DetachCodeDiagnostics(ctx, revoked.Context); err != nil {
		t.Fatal(err)
	}
	if owner.projectWatchers() != 0 {
		t.Fatal("detach left the watcher running")
	}
	// Owner close (disconnect/host reset) joins everything.
	again, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	s = await("discovery 2", again.Context, func(s cd.Snapshot) bool { p, _ := syntax(s); return p.ProviderID != "" })
	if _, err = e.ApproveCodeDiagnostics(ctx, again.Context, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	e.ResetCodeDiagnostics()
	if owner.projectWatchers() != 0 || owner.manager.OwnedWorkers() != 0 {
		t.Fatal("reset left owned work")
	}
}

// Regression (2026-10-23 app report): a Finder-launched app inherits
// PATH=/usr/bin:/bin:/usr/sbin:/sbin, so no Go toolchain, gopls or Node is
// discovered. Previously no proposal existed and both approval buttons were
// disabled. Now the built-in go-syntax analyzer gets an explicit analyzer-only
// proposal; approving it starts the watcher, and nothing is executable.
func TestCodeDiagnosticsAnalyzerOnlyApprovalStartsWatcher(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("GOPATH", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/w\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package w\nfunc {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "only"}, CWD: dir, RepoRoot: dir}})
	owner, err := newDiagnosticsOwner(t.TempDir(), "test-host")
	if err != nil {
		t.Fatal(err)
	}
	owner.watch = cd.ProjectWatchOptions{Debounce: 20 * time.Millisecond}
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() { _ = e.Close() })
	attached, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := attached.Context
	var s cd.Snapshot
	deadline := time.Now().Add(10 * time.Second)
	for s.Proposal == nil && time.Now().Before(deadline) {
		if s, err = e.GetCodeDiagnosticsSnapshot(ctx, c); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s.Proposal == nil || len(s.Proposal.Analyzers) != 1 || s.Proposal.Analyzers[0].ProviderID != cd.GoSyntaxProviderID {
		t.Fatalf("no analyzer proposal: %+v", s)
	}
	for _, tool := range s.Proposal.Tools {
		if tool.ProviderID == cd.GoSyntaxProviderID {
			t.Fatal("analyzer listed as executable")
		}
	}
	if s.Approval.State != "required" || owner.projectWatchers() != 0 {
		t.Fatal("approved or watching before consent")
	}
	if _, err := e.ApproveCodeDiagnostics(ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		s, _ = e.GetCodeDiagnosticsSnapshot(ctx, c)
		for _, set := range s.ResultSets {
			if set.ProviderID == cd.GoSyntaxProviderID && len(set.Diagnostics) > 0 && set.Diagnostics[0].Path == "a.go" {
				if owner.projectWatchers() != 1 {
					t.Fatal("watcher not owned")
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no syntax finding after approval: %+v", s)
}
