package files

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func blameRepo(t *testing.T) (string, func(...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.name", "Blame Author")
	git("config", "user.email", "blame@example.test")
	git("config", "commit.gpgsign", "false")
	return root, git
}

func TestBlameFile(t *testing.T) {
	root, git := blameRepo(t)
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("sub/file.txt", "first\nsecond\n")
	git("add", ".")
	git("commit", "-m", "Initial summary")
	commit := git("rev-parse", "HEAD")
	ctx := context.Background()
	for _, dir := range []string{root, filepath.Join(root, "sub")} {
		path := "file.txt"
		if dir == root {
			path = "sub/file.txt"
		}
		got, err := BlameFile(ctx, dir, path, 2)
		want := BlameLine{Line: 2, Commit: commit, Author: "Blame Author", AuthorTime: 1700000000, Summary: "Initial summary"}
		if err != nil || got != want {
			t.Fatalf("blame = %+v, %v; want %+v", got, err, want)
		}
	}
	write("sub/file.txt", "first\nmodified\nappended")
	for _, line := range []int{2, 3} {
		got, err := BlameFile(ctx, root, "sub/file.txt", line)
		if err != nil || got != uncommittedBlame(line) {
			t.Fatalf("modified blame = %+v, %v", got, err)
		}
	}
	got, err := BlameFile(ctx, root, "sub/file.txt", 1)
	if err != nil || got.Commit != commit || got.Uncommitted {
		t.Fatalf("unchanged line = %+v, %v", got, err)
	}
	// A glob-style name exercises --literal-pathspecs, but '[' ']' and '*'
	// cannot appear in a Windows filename at all, so the case is Unix-only.
	literalNames := []string{"new.txt", "-option.txt", "[literal]*.txt"}
	if runtime.GOOS == "windows" {
		literalNames = []string{"new.txt", "-option.txt"}
	}
	for _, path := range literalNames {
		write(path, "new\n")
		for _, staged := range []bool{false, true} {
			if staged {
				git("--literal-pathspecs", "add", "--", path)
			}
			got, err := BlameFile(ctx, root, path, 1)
			if err != nil || got != uncommittedBlame(1) {
				t.Fatalf("new %s staged %v = %+v, %v", path, staged, got, err)
			}
		}
	}
	for _, line := range []int{-1, 0, 4} {
		if _, err := BlameFile(ctx, root, "sub/file.txt", line); err == nil {
			t.Fatalf("line %d accepted", line)
		}
	}
	for _, path := range []string{"../outside", filepath.Join(root, "sub/file.txt"), ""} {
		if _, err := BlameFile(ctx, root, path, 1); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("path %q error = %v", path, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err == nil {
		if _, err := BlameFile(ctx, root, "escape", 1); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("symlink error = %v", err)
		}
	}
	write("empty", "")
	write("binary", "\x00\n")
	for _, path := range []string{"missing", "sub", "empty", "binary"} {
		if _, err := BlameFile(ctx, root, path, 1); err == nil {
			t.Fatalf("invalid file %q accepted", path)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := BlameFile(cancelled, root, "sub/file.txt", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}

func TestBlameFileNewRepositoryAndNonRepository(t *testing.T) {
	root, git := blameRepo(t)
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, staged := range []bool{false, true} {
		if staged {
			git("add", ".")
		}
		got, err := BlameFile(context.Background(), root, "new.txt", 1)
		if err != nil || got != uncommittedBlame(1) {
			t.Fatalf("unborn staged %v = %+v, %v", staged, got, err)
		}
	}
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "file"), []byte("text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BlameFile(context.Background(), plain, "file", 1); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("nonrepo error = %v", err)
	}
}
