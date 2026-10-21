package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestCommitFileUploadRPC(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	client := startEditServer(t, root, paths.Roots{Cache: cache})
	if !client.Capabilities().AddFile {
		t.Fatal("capability not advertised")
	}
	staging := filepath.Join(cache, "staging")
	os.MkdirAll(staging, 0700)
	staged := filepath.Join(staging, "upload-rpc")
	os.WriteFile(staged, []byte("uploaded"), 0600)
	ctx := context.Background()
	if err := client.CommitFileUpload(ctx, root, "new", "upload-rpc"); err != nil {
		t.Fatal(err)
	}
	if got := readRPCFile(t, filepath.Join(root, "new")); got != "uploaded" {
		t.Fatalf("copy %q", got)
	}
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatal("staged upload leaked")
	}
	if err := client.AddFile(ctx, root, "bad", "/server/secret"); err == nil {
		t.Fatal("remote arbitrary source accepted")
	}
}

func TestOldServerCannotUpload(t *testing.T) {
	_, client, cleanup := setupPipePair(nil)
	defer cleanup()
	if _, err := client.Initialize(context.Background(), InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := client.CommitFileUpload(context.Background(), "/project", "new", "upload-a"); err == nil {
		t.Fatal("old server accepted upload")
	}
}
