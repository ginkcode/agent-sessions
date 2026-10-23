package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestReplaceInFilesRPC(t *testing.T) {
	root := t.TempDir()
	a := writeRPCFile(t, root, "a.txt", "foo\r\nfoo\r\n")
	b := writeRPCFile(t, root, "b.txt", "foo theirs\n")
	store := writeRPCFile(t, root, "codex-home/c.txt", "foo")
	client := startEditServer(t, root, paths.Roots{Codex: filepath.Join(root, "codex-home")})
	ctx := context.Background()

	res, err := client.ReplaceInFiles(ctx, root, files.ReplaceRequest{
		Query:       files.SearchQuery{Pattern: "f(o+)", Regex: true},
		Replacement: "b$1",
		Files: []files.ReplaceFile{
			{Path: "a.txt", ExpectedRevision: rpcRevision("foo\r\nfoo\r\n"), Matches: []files.ReplaceMatch{{Line: 2, Start: 0}}},
			{Path: "b.txt", ExpectedRevision: rpcRevision("foo\n")},
			{Path: "codex-home/c.txt", ExpectedRevision: rpcRevision("foo")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 3 || res.Replaced != 1 {
		t.Fatalf("result = %+v", res)
	}
	const want = "foo\r\nboo\r\n"
	if f := res.Files[0]; f.Status != files.ReplaceStatusReplaced || f.Replaced != 1 || f.Revision != rpcRevision(want) {
		t.Fatalf("a.txt = %+v", f)
	}
	if f := res.Files[1]; f.Status != files.ReplaceStatusConflict || f.ObservedRevision != rpcRevision("foo theirs\n") {
		t.Fatalf("b.txt = %+v", f)
	}
	if f := res.Files[2]; f.Status != files.ReplaceStatusSkipped || f.Reason != files.EditProtected {
		t.Fatalf("store = %+v", f)
	}
	for p, body := range map[string]string{a: want, b: "foo theirs\n", store: "foo"} {
		if got := readRPCFile(t, p); got != body {
			t.Errorf("%s = %q, want %q", p, got, body)
		}
	}

	// The wire shape the frontend decodes.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"status":"replaced"`, `"status":"conflict"`, `"observedRevision"`, `"reason":"protected"`, `"replaced":1}`} {
		if !strings.Contains(string(raw), s) {
			t.Errorf("wire %s lacks %s", raw, s)
		}
	}

	_, err = client.ReplaceInFiles(ctx, t.TempDir(), files.ReplaceRequest{Query: files.SearchQuery{Pattern: "foo"}})
	if err == nil || !strings.Contains(err.Error(), engine.ErrUnknownDirectory.Error()) {
		t.Fatalf("unknown directory err = %v", err)
	}
	_, err = client.ReplaceInFiles(ctx, root, files.ReplaceRequest{Query: files.SearchQuery{Pattern: "foo"}, Replacement: "a\rb"})
	if err == nil || !strings.Contains(err.Error(), files.ErrInvalidReplacement.Error()) {
		t.Fatalf("invalid replacement err = %v", err)
	}
}

// replaceCountingBackend counts the replaces that reach the server's backend.
type replaceCountingBackend struct {
	*mockBackend
	replaces atomic.Int64
}

func (b *replaceCountingBackend) ReplaceInFiles(ctx context.Context, dir string, req files.ReplaceRequest) (files.ReplaceResult, error) {
	b.replaces.Add(1)
	return b.mockBackend.ReplaceInFiles(ctx, dir, req)
}

func TestReplaceInFilesRPCRefusedWithoutCapability(t *testing.T) {
	req := files.ReplaceRequest{Query: files.SearchQuery{Pattern: "x"}, Files: []files.ReplaceFile{{Path: "f.txt", ExpectedRevision: rpcRevision("x")}}}
	ctx := context.Background()

	t.Run("older server", func(t *testing.T) {
		backend := &replaceCountingBackend{mockBackend: &mockBackend{}}
		_, client, cleanup := setupPipePair(backend)
		defer cleanup()
		res, err := client.Initialize(ctx, InitializeRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Capabilities.ReplaceFiles {
			t.Fatalf("capabilities = %+v", res.Capabilities)
		}
		if _, err := client.ReplaceInFiles(ctx, "/dir", req); !errors.Is(err, engine.ErrUnsupportedAction) || !strings.Contains(err.Error(), "replace in files") {
			t.Fatalf("ReplaceInFiles err = %v, want ErrUnsupportedAction", err)
		}
		if n := backend.replaces.Load(); n != 0 {
			t.Fatalf("server backend got %d replaces", n)
		}
	})

	t.Run("nothing written to transport", func(t *testing.T) {
		r, _ := io.Pipe()
		rec := &recordingWriter{}
		client := NewClient(r, rec)
		defer client.Close()
		if _, err := client.ReplaceInFiles(ctx, "/dir", req); !errors.Is(err, engine.ErrUnsupportedAction) {
			t.Fatalf("ReplaceInFiles err = %v, want ErrUnsupportedAction", err)
		}
		if n := rec.Len(); n != 0 {
			t.Fatalf("client wrote %d bytes", n)
		}
	})

	t.Run("wire omits false capability", func(t *testing.T) {
		raw, err := json.Marshal(Capabilities{SaveFile: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "replaceFiles") {
			t.Fatalf("capabilities wire = %s", raw)
		}
	})
}
