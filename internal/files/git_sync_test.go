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
	"time"
)

// syncFixture uses only local bare repositories under t.TempDir; nothing here
// may reach a network remote. Global/system config is isolated from the user.
type syncFixture struct {
	t                *testing.T
	bare, work, peer string
}

func syncGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func syncIsolate(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	gitOK(t, os.WriteFile(global, []byte("[user]\n\tname = Sync\n\temail = sync@example.test\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"), 0600))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH", "GIT_ASKPASS", "SSH_ASKPASS"} {
		if value, ok := os.LookupEnv(name); ok {
			t.Setenv(name, value)
			gitOK(t, os.Unsetenv(name))
		}
	}
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	syncIsolate(t)
	base := t.TempDir()
	f := &syncFixture{t: t, bare: filepath.Join(base, "remote.git"), work: filepath.Join(base, "work"), peer: filepath.Join(base, "peer")}
	syncGit(t, base, "init", "--bare", "-b", "main", f.bare)
	syncGit(t, base, "clone", "-q", f.bare, f.work)
	f.commit(f.work, "file", "base\n")
	syncGit(t, f.work, "push", "-q", "-u", "origin", "main")
	syncGit(t, base, "clone", "-q", f.bare, f.peer)
	return f
}

func (f *syncFixture) commit(dir, name, text string) {
	f.t.Helper()
	statusWrite(f.t, dir, name, text)
	syncGit(f.t, dir, "add", name)
	syncGit(f.t, dir, "commit", "-q", "-m", name+": "+strings.TrimSpace(text))
}

func syncOK(t *testing.T, root string, op GitSyncOp) GitSyncResult {
	t.Helper()
	got, err := GitSync(context.Background(), root, GitSyncRequest{Op: op})
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	return got
}

func syncCode(t *testing.T, root string, op GitSyncOp, code string) *GitSyncError {
	t.Helper()
	_, err := GitSync(context.Background(), root, GitSyncRequest{Op: op})
	var syncErr *GitSyncError
	if !errors.As(err, &syncErr) || syncErr.Code != code {
		t.Fatalf("%s: want %s, got %v", op, code, err)
	}
	if !strings.HasPrefix(err.Error(), code+": ") {
		t.Fatalf("code prefix lost: %q", err)
	}
	return syncErr
}

func TestGitSyncFetchPullAndRefusedPull(t *testing.T) {
	f := newSyncFixture(t)
	f.commit(f.peer, "file", "peer\n")
	syncGit(t, f.peer, "push", "-q")
	fetched := syncOK(t, f.work, GitSyncFetch)
	if !fetched.Status.OK || fetched.Status.Behind != 1 || fetched.Status.Ahead != 0 || fetched.Status.Upstream != "origin/main" || !strings.Contains(fetched.Summary, "Fetched origin") {
		t.Fatalf("fetch %+v", fetched)
	}
	if text, _ := os.ReadFile(filepath.Join(f.work, "file")); string(text) != "base\n" {
		t.Fatal("fetch changed the worktree")
	}
	pulled := syncOK(t, f.work, GitSyncPull)
	if pulled.Status.Behind != 0 || pulled.Status.Head == "" || !strings.Contains(pulled.Summary, "Fast-forwarded main to origin/main (1 commit)") {
		t.Fatalf("pull %+v", pulled)
	}
	if text, _ := os.ReadFile(filepath.Join(f.work, "file")); string(text) != "peer\n" {
		t.Fatalf("pull did not fast-forward: %q", text)
	}
	if again := syncOK(t, f.work, GitSyncPull); !strings.Contains(again.Summary, "already up to date") {
		t.Fatalf("noop pull %+v", again)
	}

	// Diverged: pull must refuse instead of merging or rebasing, even with pull.rebase.
	syncGit(t, f.work, "config", "pull.rebase", "true")
	f.commit(f.work, "local", "local\n")
	f.commit(f.peer, "remote", "remote\n")
	syncGit(t, f.peer, "push", "-q")
	head := syncGit(t, f.work, "rev-parse", "HEAD")
	refused := syncCode(t, f.work, GitSyncPull, GitSyncNonFastForward)
	if !strings.Contains(strings.ToLower(refused.Message), "fast-forward") || !strings.Contains(refused.Message, "terminal") {
		t.Fatalf("message %q", refused.Message)
	}
	if syncGit(t, f.work, "rev-parse", "HEAD") != head || syncGit(t, f.work, "status", "--porcelain") != "" {
		t.Fatal("refused pull changed the repository")
	}
}

func TestGitSyncPullLocalChangesAndNoUpstream(t *testing.T) {
	f := newSyncFixture(t)
	f.commit(f.peer, "file", "peer\n")
	syncGit(t, f.peer, "push", "-q")
	statusWrite(t, f.work, "file", "dirty\n")
	syncCode(t, f.work, GitSyncPull, GitSyncLocalChanges)
	if text, _ := os.ReadFile(filepath.Join(f.work, "file")); string(text) != "dirty\n" {
		t.Fatal("dirty file overwritten")
	}
	syncGit(t, f.work, "switch", "-q", "-c", "topic")
	syncCode(t, f.work, GitSyncPull, GitSyncNoUpstream)
}

func TestGitSyncPushWithAndWithoutUpstreamNeverForces(t *testing.T) {
	f := newSyncFixture(t)
	f.commit(f.work, "file", "ahead\n")
	pushed := syncOK(t, f.work, GitSyncPush)
	if pushed.Status.Ahead != 0 || syncGit(t, f.bare, "rev-parse", "main") != syncGit(t, f.work, "rev-parse", "HEAD") || !strings.Contains(pushed.Summary, "Pushed main to origin/main.") {
		t.Fatalf("push %+v", pushed)
	}

	syncGit(t, f.work, "switch", "-q", "-c", "feature/new")
	f.commit(f.work, "feature", "feature\n")
	created := syncOK(t, f.work, GitSyncPush)
	if created.Status.Upstream != "origin/feature/new" || created.Status.Ahead != 0 || !strings.Contains(created.Summary, "set it as upstream") {
		t.Fatalf("push -u %+v", created)
	}
	if syncGit(t, f.work, "config", "branch.feature/new.merge") != "refs/heads/feature/new" || syncGit(t, f.bare, "rev-parse", "feature/new") != syncGit(t, f.work, "rev-parse", "HEAD") {
		t.Fatal("upstream not recorded")
	}

	// A forcing configured refspec and a diverged remote must still be rejected.
	syncGit(t, f.work, "switch", "-q", "main")
	syncGit(t, f.work, "config", "remote.origin.push", "+refs/heads/*:refs/heads/*")
	syncGit(t, f.peer, "pull", "-q", "--ff-only")
	f.commit(f.peer, "peer", "peer\n")
	syncGit(t, f.peer, "push", "-q")
	remote := syncGit(t, f.bare, "rev-parse", "main")
	f.commit(f.work, "mine", "mine\n")
	refused := syncCode(t, f.work, GitSyncPush, GitSyncNonFastForward)
	if !strings.Contains(refused.Message, "rejected") || syncGit(t, f.bare, "rev-parse", "main") != remote {
		t.Fatalf("forced or wrong message: %q", refused.Message)
	}
}

func TestGitSyncRemoteSelectionAndRefusals(t *testing.T) {
	f := newSyncFixture(t)
	syncGit(t, f.work, "switch", "-q", "-c", "topic")
	syncGit(t, f.work, "remote", "rename", "origin", "upstream")
	if got := syncOK(t, f.work, GitSyncPush); got.Status.Upstream != "upstream/topic" {
		t.Fatalf("single non-origin remote %+v", got)
	}
	syncGit(t, f.work, "switch", "-q", "-c", "second")
	syncGit(t, f.work, "remote", "add", "other", f.bare)
	syncCode(t, f.work, GitSyncPush, GitSyncAmbiguousRemote)
	syncGit(t, f.work, "remote", "add", "origin", f.bare)
	syncGit(t, f.work, "config", "remote.pushDefault", "other")
	syncCode(t, f.work, GitSyncPush, GitSyncPushRemote)
	syncGit(t, f.work, "config", "--unset", "remote.pushDefault")
	if got := syncOK(t, f.work, GitSyncPush); got.Status.Upstream != "origin/second" {
		t.Fatalf("origin preferred %+v", got)
	}

	syncGit(t, f.work, "switch", "-q", "--detach")
	syncCode(t, f.work, GitSyncPull, GitSyncDetached)
	syncCode(t, f.work, GitSyncPush, GitSyncDetached)
	syncOK(t, f.work, GitSyncFetch)

	lone := t.TempDir()
	syncGit(t, lone, "init", "-q")
	syncCode(t, lone, GitSyncPush, GitSyncNoCommits)
	f.commit(lone, "file", "x\n")
	syncCode(t, lone, GitSyncFetch, GitSyncNoRemote)
	syncCode(t, lone, GitSyncPush, GitSyncNoRemote)
	if _, err := GitSync(context.Background(), lone, GitSyncRequest{Op: "push --force"}); err == nil {
		t.Fatal("invalid op accepted")
	}
}

func TestGitSyncPullRefusedDuringMergeOrRebase(t *testing.T) {
	f := newSyncFixture(t)
	f.commit(f.peer, "file", "peer\n")
	syncGit(t, f.peer, "push", "-q")
	f.commit(f.work, "file", "mine\n")
	syncOK(t, f.work, GitSyncFetch)
	if err := exec.Command("git", "-C", f.work, "merge", "origin/main").Run(); err == nil {
		t.Fatal("expected merge conflict")
	}
	head := syncGit(t, f.work, "rev-parse", "HEAD")
	refused := syncCode(t, f.work, GitSyncPull, GitSyncInProgress)
	if !strings.Contains(refused.Message, "merge") || syncGit(t, f.work, "rev-parse", "HEAD") != head {
		t.Fatalf("merge: %q", refused.Message)
	}
	syncGit(t, f.work, "merge", "--abort")
	if err := exec.Command("git", "-C", f.work, "rebase", "origin/main").Run(); err == nil {
		t.Fatal("expected rebase conflict")
	}
	syncCode(t, f.work, GitSyncPull, GitSyncInProgress)
	if refused := syncCode(t, f.work, GitSyncPush, GitSyncInProgress); !strings.Contains(refused.Message, "rebase") {
		t.Fatalf("rebase: %q", refused.Message)
	}
	syncOK(t, f.work, GitSyncFetch)
	syncGit(t, f.work, "rebase", "--abort")
	if syncGit(t, f.work, "rev-parse", "HEAD") != head {
		t.Fatal("repository changed")
	}
}

// fakeSSH logs its arguments and environment and fails like a rejected key.
func fakeSSH(t *testing.T, sleep bool) (dir, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script SSH stand-in")
	}
	dir = t.TempDir()
	log = filepath.Join(dir, "log")
	body := "#!/bin/sh\necho \"args=$*\" >> '" + log + "'\necho \"prompt=$GIT_TERMINAL_PROMPT\" >> '" + log + "'\n"
	body += "if (: < /dev/tty) 2>/dev/null; then echo tty=yes >> '" + log + "'; else echo tty=no >> '" + log + "'; fi\n"
	if sleep {
		body += "sleep 30\n"
	}
	body += "echo 'git@example.invalid: Permission denied (publickey).' >&2\nexit 255\n"
	gitOK(t, os.WriteFile(filepath.Join(dir, "ssh"), []byte(body), 0700))
	return dir, log
}

func TestGitSyncNonInteractiveEnvironment(t *testing.T) {
	f := newSyncFixture(t)
	dir, log := fakeSSH(t, false)
	// The SSH URL is never contacted: "ssh" resolves to the stand-in on PATH.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	syncGit(t, f.work, "remote", "set-url", "origin", "ssh://git@example.invalid/repo.git")
	read := func() string {
		data, _ := os.ReadFile(log)
		gitOK(t, os.Remove(log))
		return string(data)
	}

	auth := syncCode(t, f.work, GitSyncFetch, GitSyncAuthRequired)
	if !strings.Contains(auth.Message, "Permission denied (publickey)") || !strings.Contains(auth.Message, "terminal") {
		t.Fatalf("auth message %q", auth.Message)
	}
	if got := read(); !strings.Contains(got, "-o BatchMode=yes") || !strings.Contains(got, "prompt=0") || !strings.Contains(got, "tty=no") {
		t.Fatalf("default ssh env: %s", got)
	}

	syncGit(t, f.work, "config", "core.sshCommand", filepath.Join(dir, "ssh")+" -o Configured=yes")
	syncCode(t, f.work, GitSyncFetch, GitSyncAuthRequired)
	if got := read(); strings.Contains(got, "BatchMode") || !strings.Contains(got, "Configured=yes") {
		t.Fatalf("core.sshCommand not respected: %s", got)
	}
	syncGit(t, f.work, "config", "--unset", "core.sshCommand")

	t.Setenv("GIT_SSH", filepath.Join(dir, "ssh"))
	syncCode(t, f.work, GitSyncFetch, GitSyncAuthRequired)
	if got := read(); strings.Contains(got, "BatchMode") || !strings.Contains(got, "prompt=0") {
		t.Fatalf("GIT_SSH not respected: %s", got)
	}

	t.Setenv("GIT_SSH_COMMAND", filepath.Join(dir, "ssh")+" -o User=yes")
	syncCode(t, f.work, GitSyncPush, GitSyncAuthRequired)
	if got := read(); strings.Contains(got, "BatchMode") || !strings.Contains(got, "User=yes") {
		t.Fatalf("GIT_SSH_COMMAND not respected: %s", got)
	}
}

func TestGitSyncHTTPPromptDisabledMapsToAuth(t *testing.T) {
	err := gitSyncFailure(GitSyncPush, &gitCommandError{command: "push", output: "fatal: could not read Username for 'https://example.invalid': terminal prompts disabled"})
	var syncErr *GitSyncError
	if !errors.As(err, &syncErr) || syncErr.Code != GitSyncAuthRequired || !strings.Contains(syncErr.Message, "terminal prompts disabled") {
		t.Fatalf("%v", err)
	}
	long := gitSyncFailure(GitSyncFetch, &gitCommandError{command: "fetch", output: strings.Repeat("x", gitErrorLimit*2)})
	if len(long.Error()) > gitErrorLimit+200 {
		t.Fatal("Git output not capped")
	}
	if NewGitSyncError("bogus", "m").Code != GitSyncFailed || NewGitSyncError(GitSyncTimeout, "m").Code != GitSyncTimeout {
		t.Fatal("decoded codes")
	}
}

func TestGitSyncCancellationAndTimeoutKillTransport(t *testing.T) {
	f := newSyncFixture(t)
	dir, _ := fakeSSH(t, true)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	syncGit(t, f.work, "remote", "set-url", "origin", "ssh://git@example.invalid/repo.git")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := GitSync(ctx, f.work, GitSyncRequest{Op: GitSyncFetch})
	var syncErr *GitSyncError
	if !errors.As(err, &syncErr) || syncErr.Code != GitSyncCanceled || !errors.Is(err, context.Canceled) || time.Since(start) > 10*time.Second {
		t.Fatalf("cancel: %v after %s", err, time.Since(start))
	}

	previous := gitSyncTimeout
	gitSyncTimeout = 500 * time.Millisecond
	defer func() { gitSyncTimeout = previous }()
	start = time.Now()
	_, err = GitSync(context.Background(), f.work, GitSyncRequest{Op: GitSyncPush})
	if !errors.As(err, &syncErr) || syncErr.Code != GitSyncTimeout || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}
	// The lock is released and later work proceeds.
	syncGit(t, f.work, "remote", "set-url", "origin", f.bare)
	gitSyncTimeout = previous
	syncOK(t, f.work, GitSyncFetch)
}
