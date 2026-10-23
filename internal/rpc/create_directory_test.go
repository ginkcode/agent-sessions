//go:build linux || darwin || windows

package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestCreateDirectoryRPC(t *testing.T) {
	root := t.TempDir()
	client := startEditServer(t, root, paths.Roots{})
	if !client.Capabilities().CreateDirectory {
		t.Fatal("missing capability")
	}
	ctx := context.Background()
	for _, tc := range []CreateDirectoryParams{{Dir: root, Name: "folder"}, {Dir: root, Path: "folder", Name: "nested"}} {
		if err := client.CreateDirectory(ctx, tc.Dir, tc.Path, tc.Name); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(filepath.Join(root, "folder/nested")); err != nil || !info.IsDir() {
		t.Fatalf("directory: %v", err)
	}
	for _, tc := range []CreateDirectoryParams{{Dir: t.TempDir(), Name: "bad"}, {Dir: root, Name: "../bad"}, {Dir: root, Path: "missing", Name: "bad"}, {Dir: root, Name: ".git"}, {Dir: root, Name: "folder"}} {
		if err := client.CreateDirectory(ctx, tc.Dir, tc.Path, tc.Name); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestCreateFileRPC(t *testing.T) {
	root := t.TempDir()
	client := startEditServer(t, root, paths.Roots{})
	if !client.Capabilities().CreateFile {
		t.Fatal("missing capability")
	}
	ctx := context.Background()
	if err := client.CreateFile(ctx, root, "", "new.go"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "new.go")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("file: %v", err)
	}
	for _, tc := range []CreateDirectoryParams{{Dir: t.TempDir(), Name: "bad"}, {Dir: root, Name: "../bad"}, {Dir: root, Path: "missing", Name: "bad"}, {Dir: root, Name: ".git"}, {Dir: root, Name: "new.go"}} {
		if err := client.CreateFile(ctx, tc.Dir, tc.Path, tc.Name); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestCreateDirectoryOldServerCapability(t *testing.T) {
	_, client, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	if _, err := client.Initialize(context.Background(), InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateDirectory(context.Background(), "root", "", "new"); err == nil {
		t.Fatal("old server accepted create directory")
	}
	if err := client.CreateFile(context.Background(), "root", "", "new"); err == nil {
		t.Fatal("old server accepted create file")
	}
}
