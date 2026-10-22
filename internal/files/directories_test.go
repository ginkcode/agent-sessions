package files

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestListingIncludesEmptyDirectories(t *testing.T) {
	for _, git := range []bool{false, true} {
		t.Run(fmt.Sprint(git), func(t *testing.T) {
			root := t.TempDir()
			if git {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("git unavailable")
				}
				if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
					t.Fatalf("git init: %v: %s", err, out)
				}
				if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{"empty", "nested/empty", "ignored/hidden", "node_modules/hidden"} {
				if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			out, err := List(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"empty", "nested", "nested/empty"} {
				if !slices.Contains(out.Directories, path) {
					t.Fatalf("missing %s: %+v", path, out)
				}
			}
			hidden := "node_modules/hidden"
			if git {
				hidden = "ignored/hidden"
			}
			if slices.Contains(out.Directories, hidden) || slices.Contains(out.Directories, ".git") {
				t.Fatalf("ignored directories listed: %v", out.Directories)
			}
		})
	}
}

func TestDirectoryListingCap(t *testing.T) {
	root := t.TempDir()
	// Test the walker directly so the cap is independent of installed git.
	for i := 0; i <= MaxFiles; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%05d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	out, err := walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || len(out.Directories) != MaxFiles {
		t.Fatalf("cap: %d truncated=%v", len(out.Directories), out.Truncated)
	}
}
