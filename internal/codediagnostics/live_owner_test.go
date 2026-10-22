package codediagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveOwnerDisplayedRoute(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, tools, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(root.Project().Root, "a.go"), []byte("disk must not be sent"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{ProviderID: "gopls", Executable: tools[0].Executable, Args: []string{"--fake"}, AdapterPolicy: "fake-only"}
	regs := []Registration{liveRegistration(ProviderState{ProviderID: "gopls", Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}}, cfg, func(file string) (string, bool) { return "go", file == "a.go" })}
	m, err := NewManager("owner", e.trust, NewSupervisor(e.trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	d := NewLiveDocuments(e.trust, m, e.mgr.options)
	t.Cleanup(func() { m.Close(); d.Close() })
	if err = d.Register(root, regs); err != nil {
		t.Fatal(err)
	}
	m.SetDocumentProvider(d)
	s, err := m.Attach(root, 1, regs)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = m.OpenDocument(ctx, s.Context, "a.go", "a", 1, "draft", "")
	code(t, err, "approvalRequired")
	if e.launches() != 0 {
		t.Fatal("unapproved launch")
	}
	s, err = m.Approve(s.Context, s.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	steps := make(chan map[string]any, 16)
	e.script = func(f *fakeSrv) {
		for {
			msg, ok := f.read()
			if !ok {
				return
			}
			switch msg["method"] {
			case "initialize":
				f.send(`{"jsonrpc":"2.0","id":1,"result":` + defaultInit + `}`)
			case "textDocument/didOpen", "textDocument/didChange":
				steps <- msg
			case "shutdown":
				f.send(`{"jsonrpc":"2.0","id":2,"result":null}`)
			case "exit":
				_ = f.Terminate()
				return
			}
		}
	}
	// The launcher snapshots script at launch. Synchronization must use the draft,
	// never the contents of a.go on disk.
	if _, err = m.OpenDocument(ctx, s.Context, "a.go", "a", 1, "draft", ""); err != nil {
		t.Fatal(err)
	}
	srv := <-e.started
	step := <-steps
	text := step["params"].(map[string]any)["textDocument"].(map[string]any)["text"]
	if text != "draft" {
		t.Fatal("didOpen did not use displayed draft")
	}
	docs, _ := DiscoveryDocuments(root, regs, d.Sessions)
	generation, _, err := docs.Session(root, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	v := int64(1)
	if err = m.PublishPathDiagnostics(root.Project(), "gopls", generation, "a.go", &v, []Diagnostic{{Path: "a.go", Source: "gopls", Message: "finding", Severity: "error"}}); err != nil {
		t.Fatal(err)
	}
	_ = srv.Terminate()
	deadline := time.After(5 * time.Second)
	for d.Sessions.Info(root, cfg).Phase != phaseFailed {
		select {
		case <-deadline:
			t.Fatal("crash not observed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	d.Refresh()
	failed, _ := m.GetSnapshot(s.Context)
	if len(failed.ResultSets) != 1 || failed.ResultSets[0].Diagnostics[0].Freshness != "stale" || failed.Providers[0].Failure == nil {
		t.Fatal("failure did not retain stale result", failed)
	}
	// Explicit recovery starts a new transport. A new displayed lifecycle supplies
	// its own text, not the retained disk/mirror text, and reuses exactly one owner.
	if _, err = m.CloseDocument(ctx, s.Context, "a"); err != nil {
		t.Fatal(err)
	}
	server, _ := docs.Resolve(root, "a.go")
	if _, err = d.Sessions.Retry(ctx, root, server.Config); err != nil {
		t.Fatal(err)
	}
	if _, err = m.OpenDocument(ctx, s.Context, "a.go", "b", 1, "new draft", ""); err != nil {
		t.Fatal(err)
	}
	step = <-steps
	if step["params"].(map[string]any)["textDocument"].(map[string]any)["text"] != "new draft" {
		t.Fatal("reopen lost draft")
	}
	if e.launches() != 2 {
		t.Fatal("duplicate owner")
	}
	if err = m.PublishPathDiagnostics(root.Project(), "gopls", generation, "a.go", &v, nil); err == nil {
		t.Fatal("old generation accepted")
	}
	if _, err = m.CloseDocument(ctx, s.Context, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.OpenDocument(ctx, s.Context, "a.go", "c", 1, "new draft", ""); err != nil {
		t.Fatal(err)
	}
	<-steps
	if e.launches() != 2 {
		t.Fatal("idle reuse launched duplicate")
	}
	current, _, _ := docs.Session(root, "a.go")
	if err = m.PublishPathDiagnostics(root.Project(), "gopls", current, "a.go", &v, nil); err == nil {
		t.Fatal("reused URI certified")
	}
	if _, err = m.CloseDocument(ctx, s.Context, "c"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(idleGrace)
	deadline = time.After(5 * time.Second)
	for d.Sessions.Info(root, cfg).Phase != phaseStopped {
		select {
		case <-deadline:
			t.Fatal("idle grace did not stop session")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err = m.OpenDocument(ctx, s.Context, "a.go", "d", 1, "after idle draft", ""); err != nil {
		t.Fatal(err)
	}
	<-steps
	isolated, _, _ := docs.Session(root, "a.go")
	if isolated == current || e.launches() != 3 {
		t.Fatal("idle expiry did not isolate new transport")
	}
	if err = m.PublishPathDiagnostics(root.Project(), "gopls", isolated, "a.go", &v, nil); err != nil {
		t.Fatal("fresh transport rejected", err)
	}
	if _, err = m.Revoke(s.Context); err != nil {
		t.Fatal(err)
	}
	d.Revoke(root)
	if d.Sessions.CurrentGeneration(root, server.Config, current) {
		t.Fatal("revoke retained session")
	}
}
