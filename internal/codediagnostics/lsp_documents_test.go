package codediagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type captured struct {
	mu     sync.Mutex
	frames []map[string]any
}

func (c *captured) add(m map[string]any) { c.mu.Lock(); c.frames = append(c.frames, m); c.mu.Unlock() }

func (c *captured) methods() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, f := range c.frames {
		if s, _ := f["method"].(string); strings.HasPrefix(s, "textDocument/") {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

func (c *captured) param(i int) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, f := range c.frames {
		if s, _ := f["method"].(string); strings.HasPrefix(s, "textDocument/") {
			if n == i {
				return f["params"].(map[string]any)
			}
			n++
		}
	}
	return nil
}

func TestSessionDocumentsAgainstFakeServer(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	cfg.ProviderID = goplsProviderID
	e.approve(root, cfg)
	got := &captured{}
	e.script = func(f *fakeSrv) {
		for {
			m, ok := f.read()
			if !ok {
				return
			}
			got.add(m)
			id, _ := json.Marshal(m["id"])
			switch m["method"] {
			case "initialize":
				f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + defaultInit + `}`)
			case "shutdown":
				f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":null}`)
			case "exit":
				_ = f.Terminate()
				return
			}
		}
	}
	docs := SessionDocuments{Sessions: e.mgr, Resolve: func(r AuthorizedRoot, path string) (DocumentServer, bool) {
		return DocumentServer{Config: cfg, LanguageID: "go"}, strings.HasSuffix(path, ".go")
	}}
	// No session yet: a document operation never launches a process.
	if _, _, err := docs.Session(root, "source.go"); errCode(err) != "staleContext" || e.launches() != 0 {
		t.Fatal(err, e.launches())
	}
	if _, _, err := (SessionDocuments{}).Session(root, "source.go"); errCode(err) != "unsupported" {
		t.Fatal(err)
	}
	if _, _, err := docs.Session(root, "other.txt"); errCode(err) != "unsupported" {
		t.Fatal(err)
	}
	changes := []FileChange{{Path: "source.go", Kind: 2}, {Path: "gone.go", Kind: 3}, {Path: "../escape.go", Kind: 3}, {Path: "other.txt", Kind: 2}, {Path: "missing.go", Kind: 1}, {Path: "source.go", Kind: 9}}
	// Watched-file transitions never launch a session either.
	if err := docs.WatchedFiles(context.Background(), root, changes); err == nil || e.launches() != 0 {
		t.Fatal("watched files launched or succeeded without a session", err, e.launches())
	}
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	e.next()
	if err := os.WriteFile(filepath.Join(root.Project().Root, "source.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := docs.WatchedFiles(context.Background(), root, changes); err != nil {
		t.Fatal(err)
	}
	var watched []any
	eventually(t, "watched frame", func() bool {
		got.mu.Lock()
		defer got.mu.Unlock()
		for _, f := range got.frames {
			if f["method"] == "workspace/didChangeWatchedFiles" {
				watched = f["params"].(map[string]any)["changes"].([]any)
				return true
			}
		}
		return false
	})
	// Only the contained changed file and the portable deleted path survive:
	// escapes, non-owned files, unresolvable creates and bad kinds are dropped.
	if len(watched) != 2 {
		t.Fatalf("%v", watched)
	}
	base := fileURI(root.Project().Root)
	for i, want := range []struct {
		suffix string
		kind   float64
	}{{"/source.go", 2}, {"/gone.go", 3}} {
		ev := watched[i].(map[string]any)
		uri := ev["uri"].(string)
		if ev["type"].(float64) != want.kind || !strings.HasSuffix(uri, want.suffix) || !strings.HasPrefix(uri, base) {
			t.Fatalf("%d: %v (root %s)", i, ev, base)
		}
	}

	if err := os.WriteFile(filepath.Join(root.Project().Root, "source.go"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManagerForTest(t, root)
	m.SetDocumentProvider(docs)
	snap := m.snapshotFor(t)
	ctx, c := context.Background(), snap.Context
	text := "package a\r\n// 😀\r\n"
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, text, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ChangeDocument(ctx, c, "d1", 2, text+"x"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveDocument(ctx, c, "d1", 2, "rev"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CloseDocument(ctx, c, "d1"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "frames", func() bool {
		return got.methods() == "textDocument/didOpen,textDocument/didChange,textDocument/didSave,textDocument/didClose"
	})
	open := got.param(0)["textDocument"].(map[string]any)
	if open["text"] != text || open["version"].(float64) != 1 || open["languageId"] != "go" || !strings.HasPrefix(open["uri"].(string), "file://") {
		t.Fatalf("%v", open)
	}
	change := got.param(1)
	edits := change["contentChanges"].([]any)
	rng := edits[0].(map[string]any)["range"].(map[string]any)
	if edits[0].(map[string]any)["text"] != "x" || rng["start"].(map[string]any)["line"].(float64) != 2 || change["textDocument"].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("%v", change)
	}
	save := got.param(2)
	if _, hasText := save["text"]; hasText {
		t.Fatal("didSave carried a body")
	}
	// Reuse inside grace, then expire and reopen with actual displayed text,
	// not a disk read or the original didOpen snapshot.
	if _, err := m.OpenDocument(ctx, c, "source.go", "idle", 1, "unsaved", "rev"); err != nil {
		t.Fatal(err)
	}
	before := e.mgr.Info(root, cfg).Generation
	e.mgr.Hide(root, cfg)
	if info, err := e.mgr.Attach(ctx, root, cfg); err != nil || info.Generation != before {
		t.Fatal("grace reuse", info, err)
	}
	e.mgr.Hide(root, cfg)
	e.clock.Advance(idleGrace)
	eventually(t, "expired", func() bool { return e.mgr.Info(root, cfg).Phase == phaseStopped })
	if err := m.PublishDocumentResults(c, "idle", before, nil, nil); errCode(err) != "staleContext" {
		t.Fatal("expired publication accepted", err)
	}
	if _, err := e.mgr.Attach(ctx, root, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ChangeDocument(ctx, c, "idle", 2, "actual displayed draft"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "reopened text", func() bool {
		p := got.param(5)
		return p != nil && p["textDocument"].(map[string]any)["text"] == "actual displayed draft"
	})
	// A dead session fails the change and invalidates the document.
	e.mgr.Revoke(root)
	if _, err := m.OpenDocument(ctx, c, "source.go", "d2", 1, "x", ""); err == nil {
		t.Fatal("stopped session accepted a document")
	}
}
