package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func gitOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestGitWriteFirstCommitAndPaths(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	for _, name := range []string{"file", "other", "literal[1]", "literal1", ":(glob)*", "-option"} {
		statusWrite(t, root, name, "original\n")
	}
	gitOK(t, GitStage(ctx, root, []string{"literal[1]", ":(glob)*", "-option"}))
	staged := git("diff", "--cached", "--name-only")
	if strings.Contains(staged, "literal1") || !strings.Contains(staged, "literal[1]") {
		t.Fatalf("not literal: %s", staged)
	}
	gitOK(t, GitStage(ctx, root, nil))
	statusWrite(t, root, "file", "changed after stage\n")
	gitOK(t, GitUnstage(ctx, root, nil))
	if got := git("ls-files"); got != "" {
		t.Fatalf("unborn still staged: %s", got)
	}
	data, _ := os.ReadFile(filepath.Join(root, "file"))
	if string(data) != "changed after stage\n" {
		t.Fatal("unstage deleted worktree")
	}
	if err := GitCommit(ctx, root, GitCommitRequest{Message: " \n"}); err == nil {
		t.Fatal("empty message accepted")
	}
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "first\n\nbody", StageAll: true}))
	if git("log", "-1", "--format=%B") != "first\n\nbody" {
		t.Fatal("message not preserved")
	}
	statusWrite(t, root, "file", "second\n")
	statusWrite(t, root, "other", "second\n")
	gitOK(t, GitStage(ctx, root, []string{"file", "other"}))
	gitOK(t, GitUnstage(ctx, root, []string{"file"}))
	got := repoStatus(t, root)
	for _, e := range got.Entries {
		if e.Path == "file" && e.Index != "" {
			t.Fatal("file still staged")
		}
		if e.Path == "other" && e.Index != "M" {
			t.Fatal("other unstaged")
		}
	}
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "only other"}))
	if git("show", "--format=", "--name-only", "HEAD") != "other" {
		t.Fatal("commit included unstaged file")
	}
}
func TestGitWritesSubdirectoryAndValidation(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	sub := filepath.Join(root, "sub")
	statusWrite(t, root, "sub/in", "in")
	statusWrite(t, root, "out", "out")
	gitOK(t, GitStage(ctx, sub, nil))
	if git("diff", "--cached", "--name-only") != "sub/in" {
		t.Fatal("stage-all escaped subdirectory")
	}
	gitOK(t, GitUnstage(ctx, sub, nil))
	if git("ls-files") != "" {
		t.Fatal("unborn unstage failed")
	}
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "base", StageAll: true}))
	statusWrite(t, root, "sub/in", "changed")
	statusWrite(t, root, "out", "changed")
	gitOK(t, GitStage(ctx, root, nil))
	gitOK(t, GitUnstage(ctx, sub, nil))
	if git("diff", "--cached", "--name-only") != "out" {
		t.Fatal("unstage-all escaped subdirectory")
	}
	gitOK(t, GitDiscard(ctx, EditPolicy{}, sub, GitDiscardRequest{}))
	data, _ := os.ReadFile(filepath.Join(root, "out"))
	if string(data) != "changed" {
		t.Fatal("discard escaped subdirectory")
	}
	for _, p := range []string{"../out", "nested/../../out", "/absolute", "", ".git/config", "a\x00b"} {
		if err := GitStage(ctx, root, []string{p}); err == nil {
			t.Errorf("stage accepted %q", p)
		}
		if err := GitUnstage(ctx, root, []string{p}); err == nil {
			t.Errorf("unstage accepted %q", p)
		}
		if err := GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{Paths: []string{p}}); err == nil {
			t.Errorf("discard accepted %q", p)
		}
	}
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		statusWrite(t, outside, "secret", "secret")
		gitOK(t, os.Symlink(outside, filepath.Join(root, "link")))
		if err := GitStage(ctx, root, []string{"link/secret"}); err == nil {
			t.Fatal("link escape accepted")
		}
	}
}
func TestGitDiscardProtectionAndDrafts(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	for _, p := range []string{"file", "deleted", "auth.json", ".aws/config", "store/data"} {
		statusWrite(t, root, p, "base\n")
	}
	git("add", ".")
	git("commit", "-m", "base")
	statusWrite(t, root, "file", "staged\n")
	git("add", "file")
	statusWrite(t, root, "file", "worktree\n")
	gitOK(t, GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{Paths: []string{"file"}}))
	data, _ := os.ReadFile(filepath.Join(root, "file"))
	if string(data) != "staged\n" {
		t.Fatal("discard did not restore from index")
	}
	gitOK(t, os.Remove(filepath.Join(root, "deleted")))
	gitOK(t, GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{Paths: []string{"deleted"}}))
	if _, err := os.Stat(filepath.Join(root, "deleted")); err != nil {
		t.Fatal(err)
	}
	statusWrite(t, root, "new/untracked", "new")
	gitOK(t, GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{Paths: []string{"new"}}))
	if _, err := os.Stat(filepath.Join(root, "new/untracked")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("untracked not deleted")
	}
	statusWrite(t, root, "file", "draft\n")
	if err := GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{Paths: []string{"file"}, DraftPaths: []string{"file"}}); err == nil || !strings.Contains(err.Error(), "unsaved draft") {
		t.Fatalf("draft accepted: %v", err)
	}
	for _, p := range []string{"auth.json", ".aws/config", "store/data"} {
		statusWrite(t, root, p, "changed\n")
		policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "store")}}
		if err := GitDiscard(ctx, policy, root, GitDiscardRequest{Paths: []string{p}}); !errors.Is(err, ErrEditProtected) {
			t.Errorf("protected %s: %v", p, err)
		}
		data, _ := os.ReadFile(filepath.Join(root, p))
		if string(data) != "changed\n" {
			t.Fatal("protected file changed")
		}
	}
	statusWrite(t, root, ".credentials-new", "secret")
	if err := GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{Paths: []string{".credentials-new"}}); !errors.Is(err, ErrEditProtected) {
		t.Fatal(err)
	}
	// Whole-request preflight must refuse before changing any selected safe file.
	if err := GitDiscard(ctx, EditPolicy{}, root, GitDiscardRequest{}); err == nil {
		t.Fatal("discard-all protected accepted")
	}
	data, _ = os.ReadFile(filepath.Join(root, "file"))
	if string(data) != "draft\n" {
		t.Fatal("partial discard before policy failure")
	}
}
func TestGitAmendSafety(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	statusWrite(t, root, "file", "base")
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "base", StageAll: true}))
	if err := GitCommit(ctx, root, GitCommitRequest{Message: "amend", Amend: true}); err == nil {
		t.Fatal("amend without upstream accepted")
	}
	git("branch", "upstream")
	git("branch", "--set-upstream-to=upstream")
	if err := GitCommit(ctx, root, GitCommitRequest{Message: "amend", Amend: true}); err == nil {
		t.Fatal("published amend accepted")
	}
	statusWrite(t, root, "file", "local")
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "local", StageAll: true}))
	before := git("rev-parse", "HEAD")
	gitOK(t, GitCommit(ctx, root, GitCommitRequest{Message: "amended", Amend: true}))
	if git("rev-parse", "HEAD") == before || git("log", "-1", "--format=%s") != "amended" || git("rev-list", "--count", "upstream..HEAD") != "1" {
		t.Fatal("amend failed")
	}
}
func TestGitHookFailureAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hooks")
	}
	root, git := blameRepo(t)
	ctx := context.Background()
	statusWrite(t, root, "file", "base")
	hooks := filepath.Join(t.TempDir(), "hooks")
	gitOK(t, os.Mkdir(hooks, 0700))
	git("config", "core.hooksPath", hooks)
	hook := filepath.Join(hooks, "pre-commit")
	gitOK(t, os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'hook refuses commit\\n' >&2\nexit 1\n"), 0700))
	err := GitCommit(ctx, root, GitCommitRequest{Message: "base", StageAll: true})
	if err == nil || !strings.Contains(err.Error(), "hook refuses commit") {
		t.Fatalf("hook failure: %v", err)
	}
	marker := filepath.Join(root, "late-hook-write")
	gitOK(t, os.WriteFile(hook, []byte("#!/bin/sh\nsleep 1\nprintf late > '"+marker+"'\n"), 0700))
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = GitCommit(deadline, root, GitCommitRequest{Message: "timeout"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout: %v duration %v", err, time.Since(start))
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("hook survived command cancellation")
	}
	gitOK(t, os.WriteFile(hook, []byte("#!/bin/sh\ni=0; while [ $i -lt 1000 ]; do printf '0123456789' >&2; i=$((i+1)); done; exit 1\n"), 0700))
	err = GitCommit(ctx, root, GitCommitRequest{Message: "bounded"})
	if err == nil || len(err.Error()) > gitErrorLimit+32 {
		t.Fatalf("unbounded error length %d", len(err.Error()))
	}
}
func TestGitWriteSerialization(t *testing.T) {
	root, git := blameRepo(t)
	ctx := context.Background()
	statusWrite(t, root, "file", "base")
	git("add", ".")
	git("commit", "-m", "base")
	common := git("rev-parse", "--path-format=absolute", "--git-common-dir")
	key, err := filepath.EvalSymlinks(common)
	gitOK(t, err)
	unlock, err := lockGitWrite(ctx, key)
	gitOK(t, err)
	defer unlock()
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := GitStage(deadline, root, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write ignored repo lock: %v", err)
	}
	// A different repository must remain writable.
	other, _ := blameRepo(t)
	statusWrite(t, other, "file", "new")
	gitOK(t, GitStage(ctx, other, nil))
}

func TestGitCommitHonorsSigningAndNoninteractiveEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook")
	}
	root, git := blameRepo(t)
	statusWrite(t, root, "file", "base")
	hooks := filepath.Join(t.TempDir(), "hooks")
	gitOK(t, os.Mkdir(hooks, 0700))
	git("config", "core.hooksPath", hooks)
	script := "#!/bin/sh\n[ \"$GIT_TERMINAL_PROMPT\" = 0 ] && [ -n \"$GIT_EDITOR\" ] && [ \"$GIT_SEQUENCE_EDITOR\" = true ] || { printf 'interactive env missing' >&2; exit 1; }\nprintf 'verified hook' > '" + filepath.Join(root, "hook-ran") + "'\n"
	gitOK(t, os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(script), 0700))
	gitOK(t, GitCommit(context.Background(), root, GitCommitRequest{Message: "base", StageAll: true}))
	if _, err := os.Stat(filepath.Join(root, "hook-ran")); err != nil {
		t.Fatal("configured hook did not run")
	}
	git("config", "commit.gpgsign", "true")
	git("config", "gpg.program", filepath.Join(root, "missing-signing-program"))
	statusWrite(t, root, "file", "next")
	if err := GitCommit(context.Background(), root, GitCommitRequest{Message: "signed", StageAll: true}); err == nil {
		t.Fatal("signing configuration bypassed")
	}
	if git("log", "-1", "--format=%s") != "base" {
		t.Fatal("failed signed commit changed HEAD")
	}
}
