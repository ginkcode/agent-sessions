package files

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func statusWrite(t *testing.T, root, path, text string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func repoStatus(t *testing.T, root string) RepoStatus {
	t.Helper()
	out, err := GitRepoStatus(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestRepoStatusChanges(t *testing.T) {
	root, git := blameRepo(t)
	initial := repoStatus(t, root)
	if !initial.OK || initial.HasCommits || initial.Detached || initial.Branch == "" || len(initial.Entries) != 0 {
		t.Fatalf("unborn: %+v", initial)
	}
	for _, p := range []string{"staged", "unstaged", "both", "deleted", "old name\nwith tab\t", "sub/inside", "outside"} {
		statusWrite(t, root, p, "base\n")
	}
	git("add", ".")
	git("commit", "-m", "base")
	statusWrite(t, root, "staged", "staged\n")
	statusWrite(t, root, "both", "staged\n")
	git("add", "staged", "both")
	statusWrite(t, root, "unstaged", "unstaged\n")
	statusWrite(t, root, "both", "worktree\n")
	os.Remove(filepath.Join(root, "deleted"))
	git("mv", "old name\nwith tab\t", "new name\nwith tab\t")
	statusWrite(t, root, "untracked space\n\t", "new\n")
	statusWrite(t, root, "sub/inside", "change\n")
	statusWrite(t, root, "outside", "change\n")
	got := repoStatus(t, root)
	entries := map[string]GitChange{}
	for _, e := range got.Entries {
		entries[e.Path] = e
	}
	for p, xy := range map[string]string{"staged": "M/", "unstaged": "/M", "both": "M/M", "deleted": "/D", "untracked space\n\t": "/?", "new name\nwith tab\t": "R/"} {
		e, ok := entries[p]
		if !ok || e.Index+"/"+e.Worktree != xy {
			t.Errorf("%q: %+v want %s", p, e, xy)
		}
	}
	if entries["new name\nwith tab\t"].OriginalPath != "old name\nwith tab\t" {
		t.Fatal(entries)
	}
	sub := repoStatus(t, filepath.Join(root, "sub"))
	if len(sub.Entries) != 1 || sub.Entries[0].Path != "inside" {
		t.Fatalf("sub: %+v", sub)
	}
	git("checkout", "--detach")
	detached := repoStatus(t, root)
	if !detached.Detached || detached.Branch != "" || len(detached.Head) != 7 || !detached.HasCommits {
		t.Fatalf("detached: %+v", detached)
	}
}
func TestRepoStatusUpstream(t *testing.T) {
	root, git := blameRepo(t)
	statusWrite(t, root, "base", "base")
	git("add", ".")
	git("commit", "-m", "base")
	git("branch", "upstream")
	git("branch", "--set-upstream-to=upstream")
	statusWrite(t, root, "local", "local")
	git("add", ".")
	git("commit", "-m", "local")
	current := git("branch", "--show-current")
	git("checkout", "upstream")
	statusWrite(t, root, "remote", "remote")
	git("add", ".")
	git("commit", "-m", "remote")
	git("checkout", current)
	got := repoStatus(t, root)
	if got.Ahead != 1 || got.Behind != 1 || got.Upstream != "upstream" {
		t.Fatalf("counts: %+v", got)
	}
}
func TestRepoStatusConflictAndWorktree(t *testing.T) {
	root, git := blameRepo(t)
	statusWrite(t, root, "file", "base\n")
	git("add", ".")
	git("commit", "-m", "base")
	branch := git("branch", "--show-current")
	git("checkout", "-b", "other")
	statusWrite(t, root, "file", "other\n")
	git("commit", "-am", "other")
	git("checkout", branch)
	statusWrite(t, root, "file", "ours\n")
	git("commit", "-am", "ours")
	cmd := exec.Command("git", "-C", root, "merge", "other")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected conflict")
	}
	got := repoStatus(t, root)
	if got.Operation != "merge" || len(got.Entries) != 1 || !got.Entries[0].Conflict || got.Entries[0].Index != "U" || got.Entries[0].Worktree != "U" {
		t.Fatalf("merge: %+v", got)
	}
	git("merge", "--abort")
	worktree := filepath.Join(t.TempDir(), "worktree")
	git("worktree", "add", "-b", "linked", worktree)
	gitdir := strings.TrimSpace(string(mustGitOutput(t, worktree, "rev-parse", "--absolute-git-dir")))
	for marker, op := range map[string]string{"rebase-merge": "rebase", "rebase-apply": "rebase", "CHERRY_PICK_HEAD": "cherry-pick", "REVERT_HEAD": "revert"} {
		path := filepath.Join(gitdir, marker)
		if strings.HasPrefix(marker, "rebase") {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			statusWrite(t, gitdir, marker, "test")
		}
		got := repoStatus(t, worktree)
		if got.Operation != op {
			t.Errorf("%s: %+v", marker, got)
		}
		if repoStatus(t, root).Operation != "" {
			t.Error("operation leaked to main worktree")
		}
		os.Remove(path)
	}
}
func mustGitOutput(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestRepoStatusUnavailableAndCancel(t *testing.T) {
	root := t.TempDir()
	got := repoStatus(t, root)
	if got.OK || got.Reason == "" {
		t.Fatalf("nonrepo: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GitRepoStatus(ctx, root); err == nil {
		t.Fatal("cancellation lost")
	}
	t.Setenv("PATH", t.TempDir())
	got = repoStatus(t, root)
	if got.OK || !strings.Contains(got.Reason, "Git") {
		t.Fatalf("no git: %+v", got)
	}
}
func TestRepoStatusCapAndCopyParser(t *testing.T) {
	var out strings.Builder
	out.WriteString("# branch.oid (initial)\x00# branch.head main\x00")
	for i := 0; i < MaxGitChanges+1; i++ {
		fmt.Fprintf(&out, "? sub/file %d\x00", i)
	}
	got, err := parseRepoStatus([]byte(out.String()), "/repo", "/repo/sub")
	if err != nil || !got.Truncated || len(got.Entries) != MaxGitChanges {
		t.Fatalf("cap: %d %v %v", len(got.Entries), got.Truncated, err)
	}
	data := []byte("2 C. N... 100644 100644 100644 abc def C100 sub/copy\x00outside\x00? other/file\x00")
	got, err = parseRepoStatus(data, "/repo", "/repo/sub")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].OriginalPath != "" || got.Entries[0].Index != "C" {
		t.Fatalf("copy: %+v %v", got, err)
	}
}

func TestRepoStatusSubdirectoryRenames(t *testing.T) {
	root, git := blameRepo(t)
	statusWrite(t, root, "outside", "outside content\n")
	statusWrite(t, root, "sub/leaving", "leaving content\n")
	statusWrite(t, root, "sub/origin", "rename content\n")
	git("add", ".")
	git("commit", "-m", "base")
	git("mv", "outside", "sub/arriving")
	git("mv", "sub/leaving", "left")
	git("mv", "sub/origin", "sub/new")
	got := repoStatus(t, filepath.Join(root, "sub"))
	if got.RepoRoot == "" {
		t.Fatal("missing repo root")
	}
	entries := map[string]GitChange{}
	for _, e := range got.Entries {
		entries[e.Path] = e
		if strings.HasPrefix(e.Path, "../") || strings.HasPrefix(e.OriginalPath, "../") {
			t.Fatalf("outside path exposed: %+v", e)
		}
	}
	if _, ok := entries["arriving"]; !ok {
		t.Fatalf("missing arriving file: %+v", got)
	}
	if entries["new"].OriginalPath != "origin" {
		t.Fatalf("missing internal rename: %+v", got)
	}
}
func TestRepoStatusSubmoduleXY(t *testing.T) {
	got, err := parseRepoStatus([]byte("1 .M S.MU 160000 160000 160000 abc def module\x00"), "/repo", "/repo")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Index != "" || got.Entries[0].Worktree != "M" {
		t.Fatalf("submodule XY: %+v %v", got, err)
	}
}
