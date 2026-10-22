package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// Exercise the actual asynchronous Engine discovery/approval/document route.
// The fake launcher fails without executing anything; framed successful sync and
// URI reuse are exercised through the same owner in codediagnostics tests.
func TestEngineInternalLiveOwnerGateAndFailure(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"server", "a.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "internal-live"}, CWD: dir, RepoRoot: dir}})
	var launches atomic.Int32
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		cfg := cd.ServerConfig{ProviderID: "gopls", Executable: filepath.Join(root.Project().Root, "server"), AdapterPolicy: "fake-only"}
		state := cd.ProviderState{ProviderID: "gopls", Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: cd.Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}}
		return []cd.Registration{{ProviderID: "gopls", Availability: "available", Live: &cd.LiveRegistration{State: state, Server: cfg, Resolve: func(path string) (string, bool) { return "go", path == "a.go" }}}}, nil
	}
	owner, err := newDiagnosticsOwnerWithLive(t.TempDir(), "fake-host", discover, cd.SessionOptions{Launch: func(cd.Invocation) (cd.SessionProcess, cd.SessionStreams, error) {
		launches.Add(1)
		return nil, cd.SessionStreams{}, errors.New("synthetic failure")
	}})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() { _ = e.Close() })
	ctx := context.Background()
	attached, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := attached.Context
	deadline := time.After(5 * time.Second)
	var s cd.Snapshot
	for {
		s, err = e.GetCodeDiagnosticsSnapshot(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if s.Proposal != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("discovery did not complete")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	_, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.go", "one", 1, "displayed draft", "")
	requireDiagnosticCode(t, err, "approvalRequired")
	if launches.Load() != 0 {
		t.Fatal("launched before composite approval")
	}
	if _, err = e.ApproveCodeDiagnostics(ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err = e.OpenCodeDiagnosticDocument(ctx, c, "a.go", "one", 1, "displayed draft", ""); err == nil {
		t.Fatal("fake launch failure acknowledged")
	}
	s, err = e.GetCodeDiagnosticsSnapshot(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range s.Providers {
		if p.ProviderID == "gopls" {
			found = p.Phase == "failed" && p.Failure != nil && p.Coverage.Kind == "openedDocuments"
		}
	}
	if !found || launches.Load() != 1 {
		t.Fatal("live failure missing from Engine snapshot", s)
	}
	revoked, err := e.RevokeCodeDiagnostics(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.OpenCodeDiagnosticDocument(ctx, revoked.Context, "a.go", "two", 1, "draft", "")
	requireDiagnosticCode(t, err, "approvalRequired")
	e.ResetCodeDiagnostics()
	if launches.Load() != 1 {
		t.Fatal("revoke/disconnect launched work")
	}
}
