package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestAddFileAndCommitUpload(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{CWD: root}})
	e := &Engine{svc: NewService(catalog, nil), cacheDirOverride: cache}
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "source")
	os.WriteFile(source, []byte("local"), 0600)
	if err := e.AddFile(ctx, cache, "new", source); err == nil {
		t.Fatal("unknown root accepted")
	}
	if err := e.AddFile(ctx, root, "new", source); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(cache, "staging")
	os.MkdirAll(staging, 0700)
	for i, tc := range []struct {
		dir, target string
		success     bool
	}{
		{root, "remote", true}, {root, "new", false}, {root, "../escape", false}, {cache, "unknown", false}, {root, "auth.json", false},
	} {
		token := "upload-" + string(rune('a'+i))
		staged := filepath.Join(staging, token)
		os.WriteFile(staged, []byte("remote"), 0600)
		err := e.CommitFileUpload(ctx, tc.dir, tc.target, token)
		if (err == nil) != tc.success {
			t.Fatalf("%+v: %v", tc, err)
		}
		if _, err := os.Lstat(staged); !os.IsNotExist(err) {
			t.Fatal("staged file leaked")
		}
	}
	for _, token := range []string{"../source", "download-a", "upload-../a", ""} {
		if err := e.CommitFileUpload(ctx, root, "bad", token); err == nil {
			t.Fatalf("accepted %q", token)
		}
	}
	os.Symlink(source, filepath.Join(staging, "upload-link"))
	if err := e.CommitFileUpload(ctx, root, "bad", "upload-link"); err == nil {
		t.Fatal("staged link accepted")
	}
	got, _ := os.ReadFile(source)
	if string(got) != "local" {
		t.Fatal("source changed")
	}
}
