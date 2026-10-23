package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitBranchesCreateSwitchTrack(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	list, err := GitBranches(ctx, root)
	gitOK(t, err)
	if len(list.Branches) != 0 {
		t.Fatalf("unborn list %+v", list)
	}
	statusWrite(t, root, "file", "base")
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "base", StageAll: true}))
	main := git("branch", "--show-current")
	gitOK(t, GitSwitch(ctx, root, GitSwitchRequest{Name: "feature/new", Create: true}))
	if git("branch", "--show-current") != "feature/new" {
		t.Fatal("create did not switch")
	}
	gitOK(t, GitSwitch(ctx, root, GitSwitchRequest{Name: main}))
	// Set a real remote and tracking ref without fetching or network access.
	git("remote", "add", "origin", t.TempDir())
	git("update-ref", "refs/remotes/origin/remote-only", "HEAD")
	git("update-ref", "refs/remotes/origin/"+main, "HEAD")
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+main)
	list, err = GitBranches(ctx, root)
	gitOK(t, err)
	seen := map[string]GitBranch{}
	for _, b := range list.Branches {
		seen[b.Name] = b
	}
	if !seen[main].Current || !seen["origin/remote-only"].Remote || seen["origin/"+main].Name != "" || seen["origin/HEAD"].Name != "" {
		t.Fatalf("branches: %+v", list)
	}
	// Local requests never silently infer a remote name.
	if err := GitSwitch(ctx, root, GitSwitchRequest{Name: "remote-only"}); err == nil {
		t.Fatal("DWIM inferred remote")
	}
	gitOK(t, GitSwitch(ctx, root, GitSwitchRequest{Name: "origin/remote-only", Remote: true}))
	if git("branch", "--show-current") != "remote-only" || git("rev-parse", "--abbrev-ref", "@{upstream}") != "origin/remote-only" {
		t.Fatal("remote not explicitly tracked")
	}
	list, err = GitBranches(ctx, root)
	gitOK(t, err)
	for _, b := range list.Branches {
		if b.Remote && b.Name == "origin/remote-only" {
			t.Fatal("tracked remote duplicated")
		}
	}
	// A differently named local tracking branch also suppresses its remote ref.
	git("branch", "-m", "different-local")
	list, err = GitBranches(ctx, root)
	gitOK(t, err)
	for _, b := range list.Branches {
		if b.Remote && b.Name == "origin/remote-only" {
			t.Fatal("upstream remote duplicated")
		}
	}
}
func TestGitBranchesOrderAndCap(t *testing.T) {
	root, git := blameRepo(t)
	statusWrite(t, root, "file", "base")
	git("add", ".")
	git("commit", "-m", "old")
	git("branch", "older")
	statusWrite(t, root, "file", "new")
	git("add", ".")
	cmd := exec.Command("git", "-C", root, "commit", "-m", "new")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=1800000000 +0000", "GIT_COMMITTER_DATE=1800000000 +0000")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	list, err := GitBranches(context.Background(), root)
	gitOK(t, err)
	if len(list.Branches) != 2 || !list.Branches[0].Current || list.Branches[0].CommitTime <= list.Branches[1].CommitTime {
		t.Fatalf("not recent first: %+v", list)
	}
	var records strings.Builder
	for i := 0; i < MaxGitBranches+1; i++ {
		fmt.Fprintf(&records, "refs/heads/b%d\x00\x001700000000\x00 \x00\n", i)
	}
	list, err = parseGitBranches([]byte(records.String()))
	gitOK(t, err)
	if len(list.Branches) != MaxGitBranches || !list.Truncated {
		t.Fatalf("cap %+v", list)
	}
}
func TestGitSwitchRefusesDraftsOptionsAndLocalChanges(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	statusWrite(t, root, "file", "base")
	git("add", ".")
	git("commit", "-m", "base")
	main := git("branch", "--show-current")
	git("checkout", "-b", "different")
	statusWrite(t, root, "file", "different")
	git("commit", "-am", "different")
	git("checkout", main)
	statusWrite(t, root, "file", "unsaved on disk")
	err := GitSwitch(ctx, root, GitSwitchRequest{Name: "different"})
	if err == nil || !strings.Contains(err.Error(), "overwritten") {
		t.Fatalf("local changes: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "file"))
	if string(data) != "unsaved on disk" {
		t.Fatal("local changes lost")
	}
	for _, create := range []bool{false, true} {
		err := GitSwitch(ctx, root, GitSwitchRequest{Name: "draft-block", Create: create, DraftPaths: []string{"file"}})
		if err == nil || !strings.Contains(err.Error(), "unsaved drafts") {
			t.Fatalf("draft block: %v", err)
		}
	}
	for _, name := range []string{"", "--detach", "-c", "../escape", "a..b", "@{-1}", "HEAD~1", "refs/heads/a\x00b", "a b"} {
		if err := GitSwitch(ctx, root, GitSwitchRequest{Name: name, Create: true}); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if git("branch", "--show-current") != main {
		t.Fatal("refused switch changed branch")
	}
	if err := GitSwitch(ctx, root, GitSwitchRequest{Name: "origin/test", Remote: true, Create: true}); err == nil {
		t.Fatal("contradictory request accepted")
	}
}
func TestGitBranchesAndSwitchUseRepoLock(t *testing.T) {
	root, git := blameRepo(t)
	statusWrite(t, root, "file", "base")
	git("add", ".")
	git("commit", "-m", "base")
	key, err := filepath.EvalSymlinks(git("rev-parse", "--path-format=absolute", "--git-common-dir"))
	gitOK(t, err)
	unlock, err := lockGitWrite(context.Background(), key)
	gitOK(t, err)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := GitBranches(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := GitSwitch(ctx, root, GitSwitchRequest{Name: "new", Create: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
