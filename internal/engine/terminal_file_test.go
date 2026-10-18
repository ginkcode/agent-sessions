package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestResolveTerminalFile(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "project")
	cwd := filepath.Join(nested, "sub")
	outside := t.TempDir()
	for _, dir := range []string{nested, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{filepath.Join(nested, "Makefile"), filepath.Join(cwd, "file with spaces"), filepath.Join(root, "other"), filepath.Join(outside, "secret")} {
		if err := os.WriteFile(name, []byte("text"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{CWD: nested, RepoRoot: root}})
	e := &Engine{svc: NewService(catalog, nil)}
	ctx := context.Background()
	for _, tc := range []struct{ dir, path, root, rel string }{
		{cwd, "../Makefile", nested, "Makefile"},
		{cwd, "file with spaces", nested, "sub/file with spaces"},
		{cwd, "../../other", root, "other"},
		{"", filepath.Join(nested, "Makefile"), nested, "Makefile"},
		{outside, filepath.Join(nested, "Makefile"), nested, "Makefile"},
	} {
		got, err := e.ResolveTerminalFile(ctx, tc.dir, tc.path)
		if err != nil || got.Dir != tc.root || got.Path != tc.rel {
			t.Errorf("resolve(%q, %q) = %+v, %v; want %s / %s", tc.dir, tc.path, got, err, tc.root, tc.rel)
		}
		if err == nil && got.Dir != "" {
			if _, err := e.ReadFile(ctx, got.Dir, got.Path); err != nil {
				t.Errorf("resolved file cannot be read: %v", err)
			}
		}
	}
	for _, tc := range []struct{ dir, path string }{
		{outside, "secret"}, {cwd, filepath.Join(outside, "secret")},
		{cwd, "missing"}, {cwd, "."}, {cwd, ""}, {"relative", "Makefile"},
		{cwd, filepath.Join(root+"-sibling", "secret")},
	} {
		if got, err := e.ResolveTerminalFile(ctx, tc.dir, tc.path); err == nil {
			t.Errorf("accepted %q %q: %+v", tc.dir, tc.path, got)
		}
	}
	if _, err := e.ResolveTerminalFile(ctx, outside, "secret"); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("unknown root error: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.ResolveTerminalFile(cancelled, nested, "Makefile"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if runtime.GOOS != "windows" {
		// Backslashes are literal filename characters on Unix, not separators.
		name := `literal\name`
		if err := os.WriteFile(filepath.Join(nested, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := e.ResolveTerminalFile(ctx, nested, name); err != nil || got.Path != name {
			t.Fatalf("native Unix semantics: %+v %v", got, err)
		}
	}
	for _, tc := range []struct {
		name, target string
		allowed      bool
	}{
		{"inside", filepath.Join(nested, "Makefile"), true},
		{"outside", filepath.Join(outside, "secret"), false},
		{"broader-root", filepath.Join(root, "other"), false},
		{"broken", filepath.Join(nested, "absent"), false},
	} {
		if err := os.Symlink(tc.target, filepath.Join(nested, tc.name)); err != nil {
			t.Logf("symlinks unavailable: %v", err)
			break
		}
		_, err := e.ResolveTerminalFile(ctx, nested, tc.name)
		if (err == nil) != tc.allowed {
			t.Errorf("symlink %s: %v", tc.name, err)
		}
		if tc.name == "broader-root" && !errors.Is(err, files.ErrOutsideRoot) {
			t.Errorf("must not fall back to broader catalog root: %v", err)
		}
	}
}
