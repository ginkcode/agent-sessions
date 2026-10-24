package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// gitSyncTimeout is a variable only so tests can shorten it.
var gitSyncTimeout = 120 * time.Second

type GitSyncOp string

const (
	GitSyncFetch GitSyncOp = "fetch"
	GitSyncPull  GitSyncOp = "pull"
	GitSyncPush  GitSyncOp = "push"
)

type GitSyncRequest struct {
	Op GitSyncOp `json:"op"`
}

// GitSyncResult carries the refreshed upstream/ahead/behind state. Status is
// unavailable (OK false with a Reason) only when the refresh itself failed
// after the operation succeeded.
type GitSyncResult struct {
	Op      GitSyncOp  `json:"op"`
	Summary string     `json:"summary"`
	Status  RepoStatus `json:"status"`
}

// Stable GitSyncError codes. The frontend matches them as an error prefix.
const (
	GitSyncAuthRequired    = "authRequired"
	GitSyncNonFastForward  = "nonFastForward"
	GitSyncLocalChanges    = "localChanges"
	GitSyncDetached        = "detached"
	GitSyncNoUpstream      = "noUpstream"
	GitSyncNoRemote        = "noRemote"
	GitSyncAmbiguousRemote = "ambiguousRemote"
	GitSyncPushRemote      = "pushRemote"
	GitSyncNoCommits       = "noCommits"
	GitSyncInProgress      = "inProgress"
	GitSyncTimeout         = "timeout"
	GitSyncCanceled        = "canceled"
	GitSyncFailed          = "failed"
)

var gitSyncCodes = map[string]bool{
	GitSyncAuthRequired: true, GitSyncNonFastForward: true, GitSyncLocalChanges: true,
	GitSyncDetached: true, GitSyncNoUpstream: true, GitSyncNoRemote: true,
	GitSyncAmbiguousRemote: true, GitSyncPushRemote: true, GitSyncNoCommits: true, GitSyncInProgress: true,
	GitSyncTimeout: true, GitSyncCanceled: true, GitSyncFailed: true,
}

// GitSyncError's message is a short explanation followed by Git's own bounded
// output, if any. Error() is "<code>: <message>" so the code survives Wails.
type GitSyncError struct {
	Code    string
	Message string
	err     error
}

func (e *GitSyncError) Error() string { return e.Code + ": " + e.Message }
func (e *GitSyncError) Unwrap() error { return e.err }

// NewGitSyncError rebuilds an error decoded from RPC; unknown codes are "failed".
func NewGitSyncError(code, message string) *GitSyncError {
	if !gitSyncCodes[code] {
		code = GitSyncFailed
	}
	return &GitSyncError{Code: code, Message: message}
}

func syncRefusal(code, message string) error { return &GitSyncError{Code: code, Message: message} }

// GitSync fetches, fast-forward pulls, or pushes the current branch. It never
// forces: refspecs are explicit and never prefixed with "+", so configured
// forcing push refspecs do not apply.
func GitSync(ctx context.Context, root string, req GitSyncRequest) (GitSyncResult, error) {
	result := GitSyncResult{Op: req.Op}
	if req.Op != GitSyncFetch && req.Op != GitSyncPull && req.Op != GitSyncPush {
		return result, errors.New("invalid Git sync operation")
	}
	err := withGitRepo(ctx, root, gitSyncTimeout, func(w gitWriter) error {
		env, err := gitSyncEnv(w)
		if err != nil {
			return err
		}
		w.env, w.session = env, true
		summary, err := gitSyncRun(w, req.Op)
		if err != nil {
			return err
		}
		result.Summary = summary
		status, err := GitRepoStatus(w.ctx, w.root)
		if err != nil {
			status = RepoStatus{Reason: err.Error(), Entries: []GitChange{}}
		}
		result.Status = status
		if status.OK && status.Upstream != "" && req.Op != GitSyncPull {
			result.Summary += fmt.Sprintf(" %d ahead, %d behind %s.", status.Ahead, status.Behind, status.Upstream)
		}
		return nil
	})
	if err != nil {
		return result, gitSyncFailure(req.Op, err)
	}
	return result, nil
}

// gitSyncEnv makes SSH non-interactive unless the user chose an SSH command.
// Keys, agents (SSH_AUTH_SOCK) and credential helpers keep working.
func gitSyncEnv(w gitWriter) ([]string, error) {
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if _, ok := os.LookupEnv(name); ok {
			return nil, nil
		}
	}
	configured, err := w.run("", "config", "--default", "", "--get", "core.sshCommand")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(configured)) != "" {
		return nil, nil
	}
	return []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes"}, nil
}

type gitSyncBranch struct {
	name, remote, merge string // remote/merge are empty without an upstream
}

func gitSyncRun(w gitWriter, op GitSyncOp) (string, error) {
	status, err := GitRepoStatus(w.ctx, w.root)
	if err != nil {
		return "", err
	}
	if !status.OK {
		return "", errors.New(status.Reason)
	}
	// Merges, rebases and unresolved conflicts make pull/push results
	// ambiguous; fetch only updates remote-tracking refs and stays allowed.
	if op != GitSyncFetch {
		conflicted := false
		for _, e := range status.Entries {
			conflicted = conflicted || e.Conflict
		}
		if status.Operation != "" || conflicted {
			what := status.Operation
			if what == "" {
				what = "conflict resolution"
			}
			return "", syncRefusal(GitSyncInProgress, fmt.Sprintf("Cannot %s while a %s is in progress. Finish or abort it first.", op, what))
		}
	}
	if status.Detached && op != GitSyncFetch {
		return "", syncRefusal(GitSyncDetached, fmt.Sprintf("Cannot %s with a detached HEAD. Switch to a branch first.", op))
	}
	branch := gitSyncBranch{name: status.Branch}
	if !status.Detached {
		if branch, err = gitSyncUpstream(w, status.Branch); err != nil {
			return "", err
		}
	}
	switch op {
	case GitSyncFetch:
		remote := branch.remote
		if remote == "" {
			if remote, err = gitSyncDefaultRemote(w); err != nil {
				return "", err
			}
		}
		if _, err := w.run("", "fetch", remote); err != nil {
			return "", err
		}
		return fmt.Sprintf("Fetched %s.", remote), nil
	case GitSyncPull:
		if branch.remote == "" {
			return "", syncRefusal(GitSyncNoUpstream, fmt.Sprintf("Branch %s has no upstream to pull from. Push it first to set one.", branch.name))
		}
		before, _ := w.run("", "rev-parse", "--verify", "--quiet", "HEAD")
		// Explicit --no-rebase keeps pull.rebase from turning this into a rebase.
		if _, err := w.run("", "pull", "--ff-only", "--no-rebase", branch.remote, branch.merge); err != nil {
			return "", err
		}
		after, err := w.run("", "rev-parse", "--verify", "HEAD")
		if err != nil {
			return "", err
		}
		upstream := gitSyncUpstreamName(branch)
		old, now := strings.TrimSpace(string(before)), strings.TrimSpace(string(after))
		if old == now {
			return fmt.Sprintf("%s is already up to date with %s.", branch.name, upstream), nil
		}
		count := "?"
		if old != "" {
			if n, err := w.run("", "rev-list", "--count", old+".."+now); err == nil {
				count = strings.TrimSpace(string(n))
			}
		}
		return fmt.Sprintf("Fast-forwarded %s to %s (%s %s).", branch.name, upstream, count, plural(count, "commit")), nil
	default:
		if !status.HasCommits {
			return "", syncRefusal(GitSyncNoCommits, fmt.Sprintf("Branch %s has no commits to push.", branch.name))
		}
		remote, merge, setUpstream := branch.remote, branch.merge, false
		if remote == "" {
			if remote, err = gitSyncDefaultRemote(w); err != nil {
				return "", err
			}
			merge, setUpstream = "refs/heads/"+branch.name, true
		}
		if err := gitSyncPushRemote(w, branch.name, remote); err != nil {
			return "", err
		}
		args := []string{"push"}
		if setUpstream {
			args = append(args, "-u")
		}
		if _, err := w.run("", append(args, remote, "refs/heads/"+branch.name+":"+merge)...); err != nil {
			return "", err
		}
		target := remote + "/" + strings.TrimPrefix(merge, "refs/heads/")
		if setUpstream {
			return fmt.Sprintf("Pushed %s to %s and set it as upstream.", branch.name, target), nil
		}
		return fmt.Sprintf("Pushed %s to %s.", branch.name, target), nil
	}
}

func plural(count, word string) string {
	if count == "1" {
		return word
	}
	return word + "s"
}

func gitSyncUpstreamName(b gitSyncBranch) string {
	if b.remote == "." {
		return strings.TrimPrefix(b.merge, "refs/heads/")
	}
	return b.remote + "/" + strings.TrimPrefix(b.merge, "refs/heads/")
}

func validSyncName(name string) bool {
	return name != "" && !strings.HasPrefix(name, "-") && !strings.ContainsAny(name, "\x00\n")
}

func gitSyncUpstream(w gitWriter, name string) (gitSyncBranch, error) {
	b := gitSyncBranch{name: name}
	if !validSyncName(name) {
		return b, errors.New("invalid current branch name")
	}
	ref := "refs/heads/" + name
	out, err := w.run("", "for-each-ref", "--format=%(refname)%00%(upstream:remotename)%00%(upstream:remoteref)", ref)
	if err != nil {
		return b, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 || fields[0] != ref || fields[1] == "" || fields[2] == "" {
			continue
		}
		if !validSyncName(fields[1]) || !strings.HasPrefix(fields[2], "refs/") || strings.ContainsAny(fields[2], "\x00\n:+") {
			return b, errors.New("invalid upstream configuration")
		}
		b.remote, b.merge = fields[1], fields[2]
	}
	return b, nil
}

// gitSyncDefaultRemote is the only remote, else origin; anything else is refused.
func gitSyncDefaultRemote(w gitWriter) (string, error) {
	out, err := w.run("", "remote")
	if err != nil {
		return "", err
	}
	var remotes []string
	for _, r := range strings.Split(string(out), "\n") {
		if r = strings.TrimSpace(r); r != "" {
			remotes = append(remotes, r)
		}
	}
	pick := ""
	switch {
	case len(remotes) == 0:
		return "", syncRefusal(GitSyncNoRemote, "This repository has no remote. Add one in a terminal with git remote add.")
	case len(remotes) == 1:
		pick = remotes[0]
	default:
		for _, r := range remotes {
			if r == "origin" {
				pick = r
			}
		}
		if pick == "" {
			return "", syncRefusal(GitSyncAmbiguousRemote, fmt.Sprintf("The branch has no upstream and the repository has several remotes but no origin (%s). Set an upstream in a terminal.", strings.Join(remotes, ", ")))
		}
	}
	if !validSyncName(pick) {
		return "", errors.New("invalid remote name")
	}
	return pick, nil
}

// gitSyncPushRemote refuses triangular setups instead of pushing elsewhere.
func gitSyncPushRemote(w gitWriter, branch, remote string) error {
	for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault"} {
		out, err := w.run("", "config", "--default", "", "--get", key)
		if err != nil {
			return err
		}
		if configured := strings.TrimSpace(string(out)); configured != "" {
			if configured != remote {
				return syncRefusal(GitSyncPushRemote, fmt.Sprintf("%s is set to push to %s, not %s. Push it from a terminal.", branch, configured, remote))
			}
			return nil
		}
	}
	return nil
}

var gitSyncAuthMarkers = []string{
	"authentication failed", "could not read username", "could not read password",
	"terminal prompts disabled", "permission denied (", "permission denied, please try again",
	"host key verification failed", "returned error: 401", "returned error: 403",
	"invalid username or password", "authentication required",
}
var gitSyncNonFastForwardMarkers = []string{
	"not possible to fast-forward", "diverging branches", "non-fast-forward",
	"(fetch first)", "updates were rejected because",
}
var gitSyncLocalChangeMarkers = []string{
	"would be overwritten by merge", "please commit your changes or stash them",
}

func containsAny(text string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// gitSyncFailure maps failures to stable codes while keeping Git's own words.
func gitSyncFailure(op GitSyncOp, err error) error {
	var known *GitSyncError
	if errors.As(err, &known) {
		return known
	}
	terminal := fmt.Sprintf("Run git %s in a terminal for details.", op)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &GitSyncError{Code: GitSyncTimeout, Message: fmt.Sprintf("git %s did not finish within %s. %s", op, gitSyncTimeout, terminal), err: err}
	case errors.Is(err, context.Canceled):
		return &GitSyncError{Code: GitSyncCanceled, Message: fmt.Sprintf("git %s was canceled.", op), err: err}
	}
	output := err.Error()
	var command *gitCommandError
	if errors.As(err, &command) && command.output != "" {
		output = command.output
	}
	if len(output) > gitErrorLimit {
		output = output[:gitErrorLimit]
	}
	lower := strings.ToLower(output)
	code, message := GitSyncFailed, fmt.Sprintf("git %s failed. %s", op, terminal)
	switch {
	case containsAny(lower, gitSyncAuthMarkers):
		code = GitSyncAuthRequired
		message = fmt.Sprintf("Git needs credentials or a confirmation it cannot ask for here. Run git %s in a terminal, or set up an SSH agent or credential helper.", op)
	case containsAny(lower, gitSyncLocalChangeMarkers):
		code = GitSyncLocalChanges
		message = "Local changes would be overwritten. Commit, stash or discard them first."
	case containsAny(lower, gitSyncNonFastForwardMarkers):
		code = GitSyncNonFastForward
		message = "The branch and its upstream have diverged, so this cannot fast-forward. Merge or rebase in a terminal."
		if op == GitSyncPush {
			message = "The remote has commits this branch does not. Pull, or merge or rebase in a terminal, before pushing."
		}
	}
	return &GitSyncError{Code: code, Message: message + "\n" + output, err: err}
}
