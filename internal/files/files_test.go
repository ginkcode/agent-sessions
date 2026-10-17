package files

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListWalkSkipsDependencies(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", "package main\n")
	write(t, root, "src/app.ts", "x")
	write(t, root, "node_modules/lib/index.js", "x")
	write(t, root, ".git/HEAD", "x")

	got, err := walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"main.go", "src/app.ts"}; !slices.Equal(got.Files, want) {
		t.Errorf("files = %v, want %v", got.Files, want)
	}
	if got.Git {
		t.Error("walk reported git")
	}
}

func TestListUsesGitIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	write(t, root, ".gitignore", "out/\n")
	write(t, root, "a.go", "x")
	write(t, root, "out/gen.go", "x")

	got, err := List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Git {
		t.Error("expected the git listing")
	}
	if want := []string{".gitignore", "a.go"}; !slices.Equal(got.Files, want) {
		t.Errorf("files = %v, want %v", got.Files, want)
	}
}

func TestReadKinds(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\n")
	write(t, root, "img.bin", "\x89PNG\x00\x01")
	write(t, root, "big.txt", strings.Repeat("x", MaxReadBytes+1))

	c, err := Read(root, "a.go")
	if err != nil || c.Text != "package a\n" || c.Binary || c.TooLarge {
		t.Errorf("text read = %+v, %v", c, err)
	}
	if c, err := Read(root, "img.bin"); err != nil || !c.Binary || c.Text != "" {
		t.Errorf("binary read = %+v, %v", c, err)
	}
	if c, err := Read(root, "big.txt"); err != nil || !c.TooLarge || c.Text != "" {
		t.Errorf("large read = %+v, %v", c, err)
	}
}

func TestReadStaysInsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	write(t, parent, "secret.txt", "s")
	write(t, root, "ok.txt", "ok")
	if err := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}

	for _, rel := range []string{"../secret.txt", "a/../../secret.txt", "/etc/passwd", "", "link.txt"} {
		if _, err := Read(root, rel); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Read(%q) err = %v, want ErrOutsideRoot", rel, err)
		}
	}
	if _, err := Read(root, "ok.txt"); err != nil {
		t.Errorf("Read(ok.txt) = %v", err)
	}
}
