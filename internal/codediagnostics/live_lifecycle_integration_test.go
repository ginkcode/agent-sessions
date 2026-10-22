package codediagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A notification completion barrier, not a sleep: every push has passed through
// real Content-Length framing, transport, session filtering, adapter and manager
// before the test inspects a snapshot. Only process launch and time are fake.
type liveLifecycle struct {
	*sessionEnv
	root    AuthorizedRoot
	cfg     ServerConfig
	owner   *managerHarness
	server  *fakeSrv
	bridge  *GoplsBridge
	handled chan struct{}
	notices chan string
}

func newLiveLifecycle(t *testing.T) *liveLifecycle {
	t.Helper()
	x := &liveLifecycle{handled: make(chan struct{}, 16), notices: make(chan string, 16)}
	x.sessionEnv = newEnv(t, SessionOptions{Notifications: func(n SessionNotification) {
		x.bridge.Handle(n)
		x.handled <- struct{}{}
	}})
	x.root, x.cfg = x.project()
	x.cfg.ProviderID = goplsProviderID
	x.approve(x.root, x.cfg)
	for _, p := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(x.root.Project().Root, p), []byte("disk stays untouched"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	x.owner = NewManagerForTest(t, x.root)
	x.bridge = NewGoplsBridge(x.root, x.owner.Manager)
	x.bridge.Notice = func(s string) { x.notices <- s }
	x.owner.SetDocumentProvider(SessionDocuments{Sessions: x.mgr, Resolve: func(AuthorizedRoot, string) (DocumentServer, bool) {
		return DocumentServer{Config: x.cfg, LanguageID: "go"}, true
	}})
	if _, err := x.mgr.Attach(context.Background(), x.root, x.cfg); err != nil {
		t.Fatal(err)
	}
	x.server = x.next()
	return x
}

func (x *liveLifecycle) push(t *testing.T, uri string, version any, ds any) {
	t.Helper()
	params := map[string]any{"uri": uri, "diagnostics": ds}
	if version != nil {
		params["version"] = version
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	x.server.send(string(body))
	select {
	case <-x.handled:
	case <-time.After(10 * time.Second):
		t.Fatal("notification did not finish")
	}
}

func (x *liveLifecycle) snapshot(t *testing.T) Snapshot {
	t.Helper()
	s, err := x.owner.GetSnapshot(x.owner.snap.Context)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func liveDiagnostic(message string, r Range) map[string]any {
	return map[string]any{"message": message, "severity": 1, "source": "compiler", "range": r}
}

func TestLiveLifecycleFramedReplacementAndGuards(t *testing.T) {
	x := newLiveLifecycle(t)
	ctx, c := context.Background(), x.owner.snap.Context
	text := "a😀\r\nb\r\n"
	for _, d := range []struct{ path, id string }{{"a.go", "a"}, {"b.go", "b"}} {
		if _, err := x.owner.OpenDocument(ctx, c, d.path, d.id, 1, text, "r1"); err != nil {
			t.Fatal(err)
		}
	}
	ranges := []Range{{Position{0, 1}, Position{0, 3}}, {Position{0, 3}, Position{1, 1}}, {Position{1, 1}, Position{1, 1}}}
	ds := []any{}
	for _, r := range ranges {
		ds = append(ds, liveDiagnostic("<script> & 😀", r))
	}
	x.push(t, uriOf(x.root, "a.go"), 1, ds)
	x.push(t, uriOf(x.root, "b.go"), 1, ds[:1])
	s := x.snapshot(t)
	if len(s.ResultSets) != 2 {
		t.Fatal("document ownership missing")
	}
	for i, d := range s.ResultSets[0].Diagnostics {
		if d.Range == nil || *d.Range != ranges[i] || d.Freshness != "current" || d.Basis.DocumentID != "a" {
			t.Fatalf("range %d: %+v", i, d)
		}
	}
	if _, err := x.owner.ChangeDocument(ctx, c, "a", 2, text+"draft"); err != nil {
		t.Fatal(err)
	}
	x.push(t, uriOf(x.root, "a.go"), 2, ds[:1])
	before := x.snapshot(t).Sequence
	for _, v := range []any{1, 7, "bad"} {
		x.push(t, uriOf(x.root, "a.go"), v, []any{})
	}
	x.push(t, uriOf(x.root, "missing.go"), 2, []any{})
	if x.snapshot(t).Sequence != before {
		t.Fatal("rejected publication committed")
	}
	// Wrong host/root/session generation and unknown document cannot clear a set.
	for _, project := range []Project{{HostID: "other", Root: x.root.Project().Root}, {HostID: x.root.Project().HostID, Root: "/other"}} {
		if err := x.owner.PublishPathDiagnostics(project, goplsProviderID, x.mgr.Info(x.root, x.cfg).Generation, "a.go", nil, nil); errCode(err) != "staleContext" {
			t.Fatal(err)
		}
	}
	if err := x.owner.PublishDocumentResults(c, "a", "old-generation", nil, nil); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
	if err := x.owner.PublishDocumentResults(c, "unknown", x.mgr.Info(x.root, x.cfg).Generation, nil, nil); err == nil {
		t.Fatal("unknown document accepted")
	}
	x.push(t, uriOf(x.root, "a.go"), nil, ds[:1])
	d := x.snapshot(t).ResultSets[0].Diagnostics[0]
	if d.Freshness != "uncertain" || d.Range != nil || d.Basis.Version != nil {
		t.Fatal("unversioned push became current")
	}
	x.push(t, uriOf(x.root, "a.go"), 2, []any{})
	s = x.snapshot(t)
	if len(s.ResultSets[0].Diagnostics) != 0 || len(s.ResultSets[1].Diagnostics) != 1 {
		t.Fatal("empty clear crossed ownership")
	}
	if _, err := x.owner.SaveDocument(ctx, c, "a", 2, "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.owner.CloseDocument(ctx, c, "a"); err != nil {
		t.Fatal(err)
	}
	x.push(t, uriOf(x.root, "a.go"), 2, ds)
	if len(x.snapshot(t).ResultSets) != 1 {
		t.Fatal("closed document resurrected")
	}
	if _, err := x.owner.OpenDocument(ctx, c, "a.go", "reopen", 1, text, ""); err != nil {
		t.Fatal(err)
	}
	x.push(t, uriOf(x.root, "a.go"), 2, ds)
	if len(x.snapshot(t).ResultSets) != 1 {
		t.Fatal("old version entered reopened lifecycle")
	}
	data, err := os.ReadFile(filepath.Join(x.root.Project().Root, "a.go"))
	if err != nil || string(data) != "disk stays untouched" {
		t.Fatal("document operations wrote source")
	}
}

func TestLiveLifecycleHostilePublicationAndInvalidRange(t *testing.T) {
	x := newLiveLifecycle(t)
	c := x.owner.snap.Context
	if _, err := x.owner.OpenDocument(context.Background(), c, "a.go", "a", 1, "😀\r\nx", ""); err != nil {
		t.Fatal(err)
	}
	x.push(t, "file:///outside/private.go", 1, []any{})
	x.push(t, "http://host/a.go", 1, []any{})
	for i := 0; i < 2; i++ {
		select {
		case n := <-x.notices:
			if strings.Contains(n, "private") || len(n) > 2048 {
				t.Fatal("unsafe notice")
			}
		default:
			t.Fatal("missing coverage notice")
		}
	}
	if len(x.snapshot(t).ResultSets) != 0 {
		t.Fatal("hostile path retained")
	}
	ds := []any{liveDiagnostic("reversed", Range{Position{1, 1}, Position{0, 0}}), liveDiagnostic("surrogate", Range{Position{0, 1}, Position{0, 2}})}
	x.push(t, uriOf(x.root, "a.go"), 1, ds)
	for _, d := range x.snapshot(t).ResultSets[0].Diagnostics {
		if d.Range != nil || d.LocationIssue != "invalidRange" {
			t.Fatalf("lost invalid-range explanation: %+v", d)
		}
	}
	// Go JSON's HTML/control escaping is accounted for by real snapshot events.
	x.push(t, uriOf(x.root, "a.go"), 1, []any{liveDiagnostic(strings.Repeat("<&\x00", 2000), Range{})})
	parts, err := x.owner.SnapshotEvents(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if encodedSize(p)+1024 > x.owner.limits.EventBytes {
			t.Fatal("escaped event exceeded budget")
		}
	}
}

func TestLiveLifecycleInvalidationCannotRepopulate(t *testing.T) {
	for _, mode := range []string{"revoke", "disconnect", "shutdown", "idle"} {
		t.Run(mode, func(t *testing.T) {
			x := newLiveLifecycle(t)
			c := x.owner.snap.Context
			if _, err := x.owner.OpenDocument(context.Background(), c, "a.go", "a", 1, "draft😀", ""); err != nil {
				t.Fatal(err)
			}
			x.push(t, uriOf(x.root, "a.go"), 1, []any{liveDiagnostic("finding", Range{})})
			gen := x.mgr.Info(x.root, x.cfg).Generation
			switch mode {
			case "revoke":
				s, err := x.owner.Revoke(c)
				if err != nil {
					t.Fatal(err)
				}
				x.owner.snap = s
				x.mgr.Revoke(x.root)
			case "disconnect":
				x.mgr.Disconnect(x.root.Project().HostID)
			case "shutdown":
				x.mgr.Close()
			case "idle":
				x.mgr.Hide(x.root, x.cfg)
				x.clock.Advance(idleGrace)
				// Close joins the asynchronous grace shutdown before assertions.
				x.mgr.Close()
			}
			// No transport can emit after Close/Revoke; replay an already queued adapter
			// callback to prove the manager's independent current-session guard.
			raw, _ := json.Marshal(map[string]any{"uri": uriOf(x.root, "a.go"), "version": 1, "diagnostics": []any{liveDiagnostic("late", Range{})}})
			before := x.snapshot(t)
			x.bridge.Handle(SessionNotification{Project: x.root.Project(), ProviderID: goplsProviderID, Generation: gen, Method: "textDocument/publishDiagnostics", Params: raw})
			after := x.snapshot(t)
			if after.Sequence != before.Sequence {
				t.Fatal("invalidated session republished")
			}
			if mode == "revoke" && len(after.ResultSets) != 0 {
				t.Fatal("revocation retained live results")
			}
			x.mgr.Close()
			if !closed(x.server.exited) {
				t.Fatal("owned process survived cleanup")
			}
		})
	}
}

func TestLiveLifecycleCrashRetryUsesNewGeneration(t *testing.T) {
	x := newLiveLifecycle(t)
	c := x.owner.snap.Context
	ctx := context.Background()
	if _, err := x.owner.OpenDocument(ctx, c, "a.go", "a", 1, "draft😀", ""); err != nil {
		t.Fatal(err)
	}
	x.push(t, uriOf(x.root, "a.go"), 1, []any{liveDiagnostic("before", Range{})})
	x.mgr.mu.Lock()
	old := x.mgr.entries[keyOf(x.root, x.cfg)].session
	x.mgr.mu.Unlock()
	_ = x.server.Terminate()
	select {
	case <-old.disposed:
	case <-time.After(10 * time.Second):
		t.Fatal("crashed session cleanup did not finish")
	}
	if _, err := x.mgr.Retry(ctx, x.root, x.cfg); err != nil {
		t.Fatal(err)
	}
	x.server = x.next()
	if x.mgr.Info(x.root, x.cfg).Generation == old.generation {
		t.Fatal("retry reused generation")
	}
	if err := x.owner.PublishDocumentResults(c, "a", old.generation, nil, nil); errCode(err) != "staleContext" {
		t.Fatal("old session cleared results", err)
	}
	if _, err := x.owner.ChangeDocument(ctx, c, "a", 2, "new displayed draft😀"); err != nil {
		t.Fatal(err)
	}
	x.push(t, uriOf(x.root, "a.go"), 1, []any{})
	if len(x.snapshot(t).ResultSets[0].Diagnostics) != 1 {
		t.Fatal("old version cleared results after restart")
	}
	x.push(t, uriOf(x.root, "a.go"), 2, []any{liveDiagnostic("after", Range{})})
	if x.snapshot(t).ResultSets[0].Diagnostics[0].Message != "after" {
		t.Fatal("new session failed to publish")
	}
	x.mgr.Close()
	if !closed(x.server.exited) || !closed(old.disposed) {
		t.Fatal("retry leaked process or session")
	}
}
