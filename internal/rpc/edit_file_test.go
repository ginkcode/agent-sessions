package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func rpcRevision(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeRPCFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readRPCFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// startEditServer serves a real engine whose catalog knows root, through a
// starter that advertises SaveFile as serve does.
func startEditServer(t *testing.T, root string, roots paths.Roots) *Client {
	t.Helper()
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "s1"}, CWD: root}})
	eng := engine.NewEngine(
		engine.WithService(engine.NewService(catalog, nil)),
		engine.WithRoots(roots),
		engine.WithCacheEnabled(false),
	)
	server, client, cleanup := setupPipePair(nil)
	t.Cleanup(cleanup)
	server.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return eng, Capabilities{Search: true, SaveFile: true, ReplaceFiles: true, AddFile: true, RenameFile: true, DeleteFile: true, CreateDirectory: true, CreateFile: true}, roots, nil
	})
	res, err := client.Initialize(context.Background(), InitializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Capabilities.SaveFile || !client.Capabilities().SaveFile {
		t.Fatalf("capabilities = %+v", res.Capabilities)
	}
	return client
}

func TestSaveFileRPCSavedAndConflict(t *testing.T) {
	root := t.TempDir()
	const body = "one\r\ntwo\r\n"
	p := writeRPCFile(t, root, "src/main.txt", body)
	client := startEditServer(t, root, paths.Roots{})
	ctx := context.Background()

	read, err := client.ReadFile(ctx, root, "src/main.txt")
	if err != nil {
		t.Fatal(err)
	}
	if read.Revision != rpcRevision(body) || read.Edit == nil || !read.Edit.Eligible || read.Edit.Newline != files.NewlineCRLF {
		t.Fatalf("read = %+v edit %+v", read, read.Edit)
	}

	// Saved: the result carries the new baseline in the file's own format.
	saved, err := client.SaveFile(ctx, root, files.SaveRequest{Path: "src/main.txt", Text: "one\nthree\n", ExpectedRevision: read.Revision})
	if err != nil {
		t.Fatal(err)
	}
	const want = "one\r\nthree\r\n"
	if saved.Status != files.SaveStatusSaved || saved.ObservedRevision != "" || saved.Content == nil {
		t.Fatalf("saved = %+v", saved)
	}
	c := saved.Content
	if c.Path != "src/main.txt" || c.Text != want || c.Size != int64(len(want)) || c.Revision != rpcRevision(want) {
		t.Fatalf("saved content = %+v", c)
	}
	if c.Edit == nil || !c.Edit.Eligible || c.Edit.Reason != "" || c.Edit.Newline != files.NewlineCRLF {
		t.Fatalf("saved edit = %+v", c.Edit)
	}
	if got := readRPCFile(t, p); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}

	// Conflict: the stale revision writes nothing and reports the current one.
	conflict, err := client.SaveFile(ctx, root, files.SaveRequest{Path: "src/main.txt", Text: "stale\n", ExpectedRevision: read.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Status != files.SaveStatusConflict || conflict.Content != nil || conflict.ObservedRevision != rpcRevision(want) {
		t.Fatalf("conflict = %+v", conflict)
	}
	if got := readRPCFile(t, p); got != want {
		t.Fatalf("conflict wrote file: %q", got)
	}

	// The wire shape the frontend decodes: a conflict has no content.
	raw, err := json.Marshal(conflict)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["status"] != "conflict" || wire["observedRevision"] != rpcRevision(want) {
		t.Fatalf("conflict wire = %s", raw)
	}
	if _, ok := wire["content"]; ok {
		t.Fatalf("conflict wire has content: %s", raw)
	}
}

func TestSaveFileRPCErrors(t *testing.T) {
	root, other, store := t.TempDir(), t.TempDir(), t.TempDir()
	const body = "x\n"
	otherFile := writeRPCFile(t, other, "f.txt", body)
	// The session directory holds a configured store, as when a project
	// lives above CODEX_HOME.
	storeRoot := filepath.Join(root, "codex-home")
	storeFile := writeRPCFile(t, root, "codex-home/config.txt", body)
	client := startEditServer(t, root, paths.Roots{Codex: storeRoot, Cache: store})
	ctx := context.Background()

	_, err := client.SaveFile(ctx, other, files.SaveRequest{Path: "f.txt", Text: "y\n", ExpectedRevision: rpcRevision(body)})
	if err == nil || !strings.Contains(err.Error(), engine.ErrUnknownDirectory.Error()) {
		t.Fatalf("unknown directory err = %v", err)
	}
	_, err = client.SaveFile(ctx, root, files.SaveRequest{Path: "codex-home/config.txt", Text: "y\n", ExpectedRevision: rpcRevision(body)})
	if err == nil || !strings.Contains(err.Error(), files.ErrEditProtected.Error()) {
		t.Fatalf("protected err = %v", err)
	}
	_, err = client.SaveFile(ctx, root, files.SaveRequest{Path: "codex-home/config.txt", Text: "y\n", ExpectedRevision: "sha256:nope"})
	if err == nil || !strings.Contains(err.Error(), files.ErrInvalidRevision.Error()) {
		t.Fatalf("invalid revision err = %v", err)
	}
	if got := readRPCFile(t, otherFile); got != body {
		t.Fatalf("unknown directory file changed: %q", got)
	}
	if got := readRPCFile(t, storeFile); got != body {
		t.Fatalf("protected file changed: %q", got)
	}
}

// saveCountingBackend counts the saves that reach the server's backend.
type saveCountingBackend struct {
	*mockBackend
	saves atomic.Int64
}

func (b *saveCountingBackend) SaveFile(ctx context.Context, dir string, req files.SaveRequest) (files.SaveResult, error) {
	b.saves.Add(1)
	return b.mockBackend.SaveFile(ctx, dir, req)
}

// recordingWriter records what a client writes to its transport.
type recordingWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *recordingWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Len()
}

func TestSaveFileRPCRefusedWithoutCapability(t *testing.T) {
	req := files.SaveRequest{Path: "f.txt", Text: "y\n", ExpectedRevision: rpcRevision("x\n")}
	ctx := context.Background()

	t.Run("older server", func(t *testing.T) {
		backend := &saveCountingBackend{mockBackend: &mockBackend{}}
		_, client, cleanup := setupPipePair(backend)
		defer cleanup()
		// Without a starter the server leaves SaveFile off, as an older
		// server that does not know the capability.
		res, err := client.Initialize(ctx, InitializeRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Capabilities.SaveFile {
			t.Fatalf("capabilities = %+v", res.Capabilities)
		}
		if _, err := client.SaveFile(ctx, "/dir", req); !errors.Is(err, engine.ErrUnsupportedAction) {
			t.Fatalf("SaveFile err = %v, want ErrUnsupportedAction", err)
		}
		if n := backend.saves.Load(); n != 0 {
			t.Fatalf("server backend got %d saves", n)
		}
		// Other calls still go through: the refusal is per capability.
		if _, err := client.ReadFile(ctx, "/dir", "f.txt"); err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
	})

	t.Run("nothing written to transport", func(t *testing.T) {
		// A client that never initialized has no capabilities; the refusal
		// must not even send the request.
		r, _ := io.Pipe()
		rec := &recordingWriter{}
		client := NewClient(r, rec)
		defer client.Close()
		if _, err := client.SaveFile(ctx, "/dir", req); !errors.Is(err, engine.ErrUnsupportedAction) {
			t.Fatalf("SaveFile err = %v, want ErrUnsupportedAction", err)
		}
		if n := rec.Len(); n != 0 {
			t.Fatalf("client wrote %d bytes: %q", n, rec.buf.String())
		}
	})

	t.Run("wire omits false capability", func(t *testing.T) {
		raw, err := json.Marshal(Capabilities{Search: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "saveFile") {
			t.Fatalf("capabilities wire = %s", raw)
		}
		var caps Capabilities
		if err := json.Unmarshal([]byte(`{"trash":true,"manage":true,"export":true,"import":true,"search":true}`), &caps); err != nil || caps.SaveFile {
			t.Fatalf("older server capabilities = %+v, %v", caps, err)
		}
	})
}
