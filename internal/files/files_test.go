package files

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

	c, err := Read(context.Background(), root, "a.go")
	if err != nil || c.Text != "package a\n" || c.Binary || c.TooLarge {
		t.Errorf("text read = %+v, %v", c, err)
	}
	if c, err := Read(context.Background(), root, "img.bin"); err != nil || !c.Binary || c.Text != "" {
		t.Errorf("binary read = %+v, %v", c, err)
	}
	if c, err := Read(context.Background(), root, "big.txt"); err != nil || !c.TooLarge || c.Text != "" {
		t.Errorf("large read = %+v, %v", c, err)
	}
}

func TestReadImages(t *testing.T) {
	root := t.TempDir()
	write(t, root, "logo.PNG", "\x89PNG\x00\x01")
	write(t, root, "photo.jpg", strings.Repeat("x", MaxReadBytes+1))
	write(t, root, "huge.gif", strings.Repeat("x", MaxImageBytes+1))

	c, err := Read(context.Background(), root, "logo.PNG")
	if err != nil || !c.Binary || c.Image != "image/png" || c.Data != base64.StdEncoding.EncodeToString([]byte("\x89PNG\x00\x01")) {
		t.Errorf("png read = %+v, %v", c, err)
	}
	// Larger than a text file may be, but still shown.
	if c, err := Read(context.Background(), root, "photo.jpg"); err != nil || c.TooLarge || c.Image != "image/jpeg" || c.Data == "" {
		t.Errorf("jpeg read = image %q, tooLarge %v, %v", c.Image, c.TooLarge, err)
	}
	if c, err := Read(context.Background(), root, "huge.gif"); err != nil || !c.TooLarge || c.Data != "" {
		t.Errorf("huge image read = tooLarge %v, data %d bytes, %v", c.TooLarge, len(c.Data), err)
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
		if _, err := Read(context.Background(), root, rel); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Read(%q) err = %v, want ErrOutsideRoot", rel, err)
		}
	}
	if _, err := Read(context.Background(), root, "ok.txt"); err != nil {
		t.Errorf("Read(ok.txt) = %v", err)
	}
}

func TestParseStatus(t *testing.T) {
	out := " D sub/del.txt\x00R  sub/moved.txt\x00top.txt\x00 M sub/x.txt\x00?? sub/new.txt\x00A  sub/add.txt\x00UU sub/both.txt\x00MD sub/gone.txt\x00 M other/y.txt\x00"
	got := parseStatus(out, "sub/")
	want := map[string]Status{
		"del.txt": Deleted, "moved.txt": Renamed, "x.txt": Modified, "new.txt": Untracked,
		"add.txt": Added, "both.txt": Conflict, "gone.txt": Deleted,
	}
	if len(got) != len(want) {
		t.Fatalf("status = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("status[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseHunks(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -2 +2 @@ a\n-b\n+B\n@@ -3,0 +4 @@ c\n+new\n@@ -7,2 +7,0 @@\n-g\n-h\n@@ -10,2 +9,3 @@\n"
	want := []LineChange{
		{Kind: "modified", Start: 2, Count: 1, Removed: []string{"b"}},
		{Kind: "added", Start: 4, Count: 1},
		{Kind: "deleted", Start: 7, Removed: []string{"g", "h"}},
		{Kind: "modified", Start: 9, Count: 3},
	}
	if got := parseHunks(diff); !reflect.DeepEqual(got, want) {
		t.Errorf("hunks = %v, want %v", got, want)
	}
	// A "---" line inside a hunk is a removed "--" line, not a file header.
	got := parseHunks("--- a/x\n+++ b/x\n@@ -1 +0,0 @@\n---\n\\ No newline at end of file\n")
	if want := []LineChange{{Kind: "deleted", Start: 0, Removed: []string{"--"}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("hunks = %v, want %v", got, want)
	}
}

func TestGitStatusAndChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	write(t, repo, "sub/x.txt", "a\nb\nc\nd\n")
	write(t, repo, "sub/del.txt", "k\n")
	git("add", "-A")
	git("commit", "-qm", "init")
	write(t, repo, "sub/x.txt", "a\nB\nc\nnew\nd\n")
	write(t, repo, "sub/new.txt", "u\n")
	write(t, repo, ".gitignore", ".env\nnode_modules/\n")
	write(t, repo, "sub/.env", "SECRET=1\n")
	write(t, repo, "sub/node_modules/pkg/index.js", "x\n")
	if err := os.Remove(filepath.Join(repo, "sub", "del.txt")); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(repo, "sub")
	got, err := List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Files, []string{"new.txt", "x.txt"}) {
		t.Errorf("files = %v", got.Files)
	}
	if !slices.Equal(got.Ignored, []string{".env"}) {
		t.Errorf("ignored = %v, want only .env (node_modules not walked)", got.Ignored)
	}
	want := map[string]Status{"x.txt": Modified, "new.txt": Untracked, "del.txt": Deleted}
	if len(got.Status) != len(want) {
		t.Errorf("status = %v, want %v", got.Status, want)
	}
	for k, v := range want {
		if got.Status[k] != v {
			t.Errorf("status[%q] = %q, want %q", k, got.Status[k], v)
		}
	}

	c, err := Read(context.Background(), root, "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := []LineChange{{Kind: "modified", Start: 2, Count: 1, Removed: []string{"b"}}, {Kind: "added", Start: 4, Count: 1}}
	if !reflect.DeepEqual(c.Changes, wantChanges) {
		t.Errorf("changes = %v, want %v", c.Changes, wantChanges)
	}
}
