package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// nestedRepo initializes a repository at root/rel with one committed file.
func nestedRepo(t *testing.T, root, rel string) func(...string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	write(t, root, rel+"/main.go", "package main\n")
	git("init", "-q")
	git("config", "user.name", "Nested")
	git("config", "user.email", "nested@example.test")
	git("config", "commit.gpgsign", "false")
	git("config", "core.autocrlf", "false")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	return git
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
}

func TestReadChangesInNestedRepository(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	nestedRepo(t, root, "api")
	write(t, root, "api/main.go", "package main\n\nfunc main() {}\n")
	write(t, root, "plain.txt", "x\n")
	c, err := Read(context.Background(), root, "api/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Changes) != 1 || c.Changes[0].Kind != "added" || c.Changes[0].Start != 2 || c.Changes[0].Count != 2 {
		t.Errorf("changes = %+v, want lines 2-3 added", c.Changes)
	}
	if c, err := Read(context.Background(), root, "plain.txt"); err != nil || c.Changes != nil {
		t.Errorf("plain file changes = %+v, %v; want none", c.Changes, err)
	}
}
