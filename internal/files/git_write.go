package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const gitWriteTimeout = 60 * time.Second
const gitErrorLimit = 4096

// GitDiscardRequest carries caller-known unsaved drafts. UI confirmation and
// collecting live editor drafts remain the frontend's responsibility.
type GitDiscardRequest struct {
	Paths      []string `json:"paths"`
	DraftPaths []string `json:"draftPaths,omitempty"`
}
type GitCommitRequest struct {
	Message  string `json:"message"`
	Amend    bool   `json:"amend"`
	StageAll bool   `json:"stageAll"`
}

// Locks are keyed by canonical Git common directory, including linked worktrees.
// Reference counting avoids retaining every repository ever opened.
var gitWrites = struct {
	sync.Mutex
	entries map[string]*gitWriteSlot
}{entries: map[string]*gitWriteSlot{}}

type gitWriteSlot struct {
	token chan struct{}
	refs  int
}

func lockGitWrite(ctx context.Context, key string) (func(), error) {
	gitWrites.Lock()
	slot := gitWrites.entries[key]
	if slot == nil {
		slot = &gitWriteSlot{token: make(chan struct{}, 1)}
		gitWrites.entries[key] = slot
	}
	slot.refs++
	gitWrites.Unlock()
	release := func() {
		gitWrites.Lock()
		slot.refs--
		if slot.refs == 0 {
			delete(gitWrites.entries, key)
		}
		gitWrites.Unlock()
	}
	select {
	case slot.token <- struct{}{}:
		return func() { <-slot.token; release() }, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

type gitLimitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *gitLimitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.buffer.Len()
	if len(p) > left {
		p = p[:left]
		b.overflow = true
	}
	b.buffer.Write(p)
	return n, nil
}

type gitWriter struct {
	ctx       context.Context
	root, git string
	// env is appended after the defaults. session runs network commands without
	// a controlling terminal so SSH or credential helpers cannot wait on /dev/tty.
	env     []string
	session bool
}

// gitCommandError keeps Git's bounded output for callers that classify it.
type gitCommandError struct {
	command, output string
	err             error
}

func (e *gitCommandError) Error() string {
	if e.output != "" {
		return fmt.Sprintf("git %s: %s", e.command, e.output)
	}
	return fmt.Sprintf("git %s: %v", e.command, e.err)
}
func (e *gitCommandError) Unwrap() error { return e.err }

func (w gitWriter) run(input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(w.ctx, w.git, append([]string{"-C", w.root, "--literal-pathspecs", "--no-pager"}, args...)...)
	hideConsole(cmd)
	if w.session {
		prepareGitSessionProcess(cmd)
	} else {
		prepareGitWriteProcess(cmd)
	}
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true", "GIT_PAGER=cat"), w.env...)
	cmd.Stdin = strings.NewReader(input)
	stdout := &gitLimitedBuffer{limit: 16 << 20}
	stderr := &gitLimitedBuffer{limit: gitErrorLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A hook which inherits pipes must not keep the caller blocked after timeout.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if w.ctx.Err() != nil {
		return nil, w.ctx.Err()
	}
	if err != nil {
		text := strings.TrimSpace(stderr.buffer.String())
		if text == "" {
			text = strings.TrimSpace(stdout.buffer.String())
			if len(text) > gitErrorLimit {
				text = text[:gitErrorLimit]
			}
		}
		return nil, &gitCommandError{command: args[0], output: text, err: err}
	}
	if stdout.overflow {
		return nil, errors.New("git output exceeds safety limit")
	}
	return stdout.buffer.Bytes(), nil
}

func withGitWrite(ctx context.Context, root string, action func(gitWriter) error) error {
	return withGitRepo(ctx, root, gitWriteTimeout, action)
}

// withGitRepo bounds lock waiting and every command by one timeout.
func withGitRepo(ctx context.Context, root string, timeout time.Duration, action func(gitWriter) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	w := gitWriter{ctx: ctx, root: root, git: git}
	common, err := w.run("", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	key, err := filepath.EvalSymlinks(strings.TrimSuffix(string(common), "\n"))
	if err != nil {
		return err
	}
	unlock, err := lockGitWrite(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	return action(w)
}

// gitPaths accepts deleted paths too, but never parent traversal, pathspec
// magic, or link escapes. Git is always invoked with literal pathspecs.
func gitPaths(root string, paths []string) ([]string, error) {
	if len(paths) > MaxGitChanges {
		return nil, errors.New("too many Git paths")
	}
	if len(paths) == 0 {
		return []string{"."}, nil
	}
	result := make([]string, 0, len(paths))
	for _, rel := range paths {
		if strings.ContainsRune(rel, 0) || editHasParentComponent(rel) || !filepath.IsLocal(rel) {
			return nil, ErrOutsideRoot
		}
		rel = filepath.Clean(filepath.FromSlash(rel))
		for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.EqualFold(component, ".git") {
				return nil, ErrEditProtected
			}
		}
		full, err := editResolveAncestor(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		if !editWithin(root, full) {
			return nil, ErrOutsideRoot
		}
		result = append(result, filepath.ToSlash(rel))
	}
	return result, nil
}

func GitStage(ctx context.Context, root string, paths []string) error {
	return withGitWrite(ctx, root, func(w gitWriter) error {
		p, err := gitPaths(w.root, paths)
		if err != nil {
			return err
		}
		_, err = w.run("", append([]string{"add", "-A", "--"}, p...)...)
		return err
	})
}
func GitUnstage(ctx context.Context, root string, paths []string) error {
	return withGitWrite(ctx, root, func(w gitWriter) error {
		p, err := gitPaths(w.root, paths)
		if err != nil {
			return err
		}
		status, err := GitRepoStatus(w.ctx, w.root)
		if err != nil {
			return err
		}
		if !status.OK {
			return errors.New(status.Reason)
		}
		args := []string{"restore", "--staged", "--"}
		if !status.HasCommits {
			args = []string{"rm", "--cached", "-r", "-f", "--ignore-unmatch", "--"}
		}
		_, err = w.run("", append(args, p...)...)
		return err
	})
}

func GitDiscard(ctx context.Context, policy EditPolicy, root string, req GitDiscardRequest) error {
	return withGitWrite(ctx, root, func(w gitWriter) error {
		p, err := gitPaths(w.root, req.Paths)
		if err != nil {
			return err
		}
		tracked, err := w.run("", append([]string{"ls-files", "-z", "--cached", "--"}, p...)...)
		if err != nil {
			return err
		}
		others, err := w.run("", append([]string{"ls-files", "-z", "--others", "--exclude-standard", "--"}, p...)...)
		if err != nil {
			return err
		}
		// ls-files names are project-relative when run in a subdirectory.
		collect := func(data []byte) []string {
			var out []string
			seen := map[string]bool{}
			for _, path := range strings.Split(string(data), "\x00") {
				if path != "" && !seen[path] {
					out = append(out, path)
					seen[path] = true
				}
			}
			return out
		}
		trackedPaths, otherPaths := collect(tracked), collect(others)
		all := append(append([]string{}, trackedPaths...), otherPaths...)
		if len(all) > MaxGitChanges {
			return errors.New("too many files to discard; select fewer files")
		}
		drafts, err := gitPaths(w.root, req.DraftPaths)
		if err != nil {
			return err
		}
		if len(req.DraftPaths) == 0 {
			drafts = nil
		}
		for _, rel := range all {
			for _, draft := range drafts {
				if editWithin(filepath.Join(w.root, draft), filepath.Join(w.root, rel)) {
					return fmt.Errorf("cannot discard %s: unsaved draft", rel)
				}
			}
			if err := gitDiscardPreflight(policy, w.root, rel); err != nil {
				return fmt.Errorf("cannot discard %s: %w", rel, err)
			}
		}
		if len(trackedPaths) > 0 {
			if _, err = w.run("", append([]string{"restore", "--worktree", "--"}, trackedPaths...)...); err != nil {
				return err
			}
		}
		for _, rel := range otherPaths {
			if err := Delete(w.ctx, policy, w.root, rel); err != nil {
				return err
			}
		}
		return nil
	})
}

func gitDiscardPreflight(policy EditPolicy, root, rel string) error {
	if _, err := gitPaths(root, []string{rel}); err != nil {
		return err
	}
	target := filepath.Join(root, filepath.FromSlash(rel))
	real, err := editResolveAncestor(target)
	if err != nil {
		return err
	}
	if err := policy.refuse(false, target, real); err != nil {
		return err
	}
	// Refuse links and submodules/directories. A deleted regular file is allowed;
	// all existing parents must still be ordinary directories.
	parts := strings.Split(filepath.Clean(filepath.FromSlash(rel)), string(filepath.Separator))
	for i := range parts {
		info, err := os.Lstat(filepath.Join(root, filepath.Join(parts[:i+1]...)))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return ErrEditLink
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return ErrEditTarget
			}
		} else if !info.IsDir() {
			return ErrEditTarget
		}
	}
	return nil
}

func GitCommit(ctx context.Context, root string, req GitCommitRequest) error {
	if strings.TrimSpace(req.Message) == "" || strings.ContainsRune(req.Message, 0) {
		return errors.New("commit message must not be empty or contain NUL")
	}
	return withGitWrite(ctx, root, func(w gitWriter) error {
		if req.Amend {
			shallow, err := w.run("", "rev-parse", "--is-shallow-repository")
			if err != nil || strings.TrimSpace(string(shallow)) != "false" {
				return errors.New("cannot amend safely: complete repository history is unavailable")
			}
			// Fail closed when no upstream exists or comparison cannot be completed.
			head, err := w.run("", "rev-parse", "--verify", "HEAD")
			if err != nil {
				return err
			}
			upstream, err := w.run("", "rev-parse", "--verify", "@{upstream}")
			if err != nil {
				return errors.New("cannot amend safely: upstream is unavailable")
			}
			commits, err := w.run("", "rev-list", "--max-count=1", strings.TrimSpace(string(upstream))+".."+strings.TrimSpace(string(head)))
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(commits)) == "" {
				return errors.New("cannot amend: last commit is already on the upstream")
			}
		}
		if req.StageAll {
			if _, err := w.run("", "add", "-A", "--", "."); err != nil {
				return err
			}
		}
		args := []string{"commit", "-F", "-"}
		if req.Amend {
			args = append(args, "--amend")
		}
		_, err := w.run(req.Message, args...)
		return err
	})
}
