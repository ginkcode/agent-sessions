//go:build linux || darwin || windows

package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestRenameFileRPC(t *testing.T) {
	root := t.TempDir()
	writeRPCFile(t, root, "folder/old", "data")
	client := startEditServer(t, root, paths.Roots{})
	if !client.Capabilities().RenameFile {
		t.Fatal("missing capability")
	}
	ctx := context.Background()
	if err := client.RenameFile(ctx, root, "folder/old", "new"); err != nil {
		t.Fatal(err)
	}
	if err := client.RenameFile(ctx, root, "folder", "renamed"); err != nil {
		t.Fatal(err)
	}
	if got := readRPCFile(t, filepath.Join(root, "renamed/new")); got != "data" {
		t.Fatal(got)
	}
	for _, tc := range []RenameFileParams{
		{Dir: t.TempDir(), Path: "renamed/new", Name: "bad"},
		{Dir: root, Path: "renamed/new", Name: "../bad"},
		{Dir: root, Path: ".", Name: "bad"},
		{Dir: root, Path: "renamed/new", Name: "auth.json"},
	} {
		if err := client.RenameFile(ctx, tc.Dir, tc.Path, tc.Name); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "renamed/new")); err != nil {
		t.Fatal(err)
	}
}

func TestRenameFileOldServerCapability(t *testing.T) {
	_, client, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	if _, err := client.Initialize(context.Background(), InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := client.RenameFile(context.Background(), "root", "file", "new"); err == nil {
		t.Fatal("old server accepted rename")
	}
}
