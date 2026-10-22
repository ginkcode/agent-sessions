package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestDeleteFileRPC(t *testing.T) {
	root := t.TempDir()
	writeRPCFile(t, root, "folder/file", "data")
	writeRPCFile(t, root, "folder/deep/file", "data")
	writeRPCFile(t, root, "protected/.git/config", "data")
	client := startEditServer(t, root, paths.Roots{})
	if !client.Capabilities().DeleteFile {
		t.Fatal("missing capability")
	}
	ctx := context.Background()
	for _, tc := range []DeleteFileParams{
		{Dir: t.TempDir(), Path: "folder/file"},
		{Dir: root, Path: "folder/../folder/file"},
		{Dir: root, Path: "."},
		{Dir: root, Path: "protected"},
	} {
		if err := client.DeleteFile(ctx, tc.Dir, tc.Path); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	for _, path := range []string{"folder/file", "folder"} {
		if err := client.DeleteFile(ctx, root, path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("target remains: %v", err)
		}
	}
	if got := readRPCFile(t, filepath.Join(root, "protected/.git/config")); got != "data" {
		t.Fatal(got)
	}
}

func TestDeleteFileOldServerCapability(t *testing.T) {
	_, client, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	if _, err := client.Initialize(context.Background(), InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteFile(context.Background(), "root", "file"); err == nil {
		t.Fatal("old server accepted delete")
	}
}
