package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestFileActionAuthorizationAndStaging(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{CWD: root}})
	e := &Engine{svc: NewService(catalog, nil), cacheDirOverride: cache}
	ctx := context.Background()
	// Blank-space context actions send '.' to select the authorized project root.
	resolved, err := e.ResolveFile(ctx, root, ".")
	if err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != realRoot {
		t.Fatalf("root resolution: %q, want %q: %v", resolved, realRoot, err)
	}
	if _, err := e.ResolveFile(ctx, cache, "."); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("authorization: %v", err)
	}
	if _, err := e.PrepareFileDownload(ctx, cache, ".", "download-no"); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("download authorization: %v", err)
	}
	for _, token := range []string{"../bad", "download-../bad", "other", ""} {
		if _, err := e.PrepareFileDownload(ctx, root, "file", token); err == nil {
			t.Errorf("accepted token %q", token)
		}
	}
	got, err := e.PrepareFileDownload(ctx, root, "file", "download-safe")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "file" || got.Token != "download-safe" {
		t.Fatalf("metadata %+v", got)
	}
	p := filepath.Join(cache, "staging", "download-safe")
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "original" {
		t.Fatalf("artifact %q %v", data, err)
	}
	if _, err := e.PrepareFileDownload(ctx, root, "file", "download-safe"); err == nil {
		t.Fatal("overwrote existing token")
	}
	if err := os.Symlink(root, filepath.Join(root, "link")); err == nil {
		if _, err := e.PrepareFileDownload(ctx, root, ".", "download-fail"); err == nil {
			t.Fatal("archived symlink")
		}
		if _, err := os.Stat(filepath.Join(cache, "staging", "download-fail")); !os.IsNotExist(err) {
			t.Fatalf("failed artifact not cleaned: %v", err)
		}
	}
}
