package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const MaxGitBranches = 500

type GitBranch struct {
	Name       string `json:"name"`
	Remote     bool   `json:"remote"`
	Current    bool   `json:"current"`
	Upstream   string `json:"upstream,omitempty"`
	CommitTime int64  `json:"commitTime"`
}
type GitBranchList struct {
	Branches  []GitBranch `json:"branches"`
	Truncated bool        `json:"truncated"`
}
type GitSwitchRequest struct {
	Name   string `json:"name"`
	Create bool   `json:"create"`
	// Remote explicitly selects refs/remotes/Name instead of a same-named local branch.
	Remote bool `json:"remote,omitempty"`
	// Any live unsaved draft blocks branch changes, including draft-only new files.
	DraftPaths []string `json:"draftPaths,omitempty"`
}

// GitBranches uses the same bounded runner/lock as writes, yielding a consistent
// branch snapshot even while another app request switches a linked worktree.
func GitBranches(ctx context.Context, root string) (GitBranchList, error) {
	result := GitBranchList{Branches: []GitBranch{}}
	err := withGitWrite(ctx, root, func(w gitWriter) error {
		out, err := w.run("", "for-each-ref", "--sort=refname", "--sort=-committerdate", "--format=%(refname)%00%(upstream)%00%(committerdate:unix)%00%(HEAD)%00%(symref)", "refs/heads/", "refs/remotes/")
		if err != nil {
			return err
		}
		result, err = parseGitBranches(out)
		return err
	})
	return result, err
}
func parseGitBranches(out []byte) (GitBranchList, error) {
	result := GitBranchList{Branches: []GitBranch{}}
	var all []GitBranch
	locals, upstreams := map[string]bool{}, map[string]bool{}
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		fields := strings.Split(string(line), "\x00")
		if len(fields) != 5 {
			return result, errors.New("invalid Git branch record")
		}
		if fields[4] != "" {
			continue
		} // e.g. origin/HEAD symbolic alias
		b := GitBranch{Current: fields[3] == "*"}
		switch {
		case strings.HasPrefix(fields[0], "refs/heads/"):
			b.Name = strings.TrimPrefix(fields[0], "refs/heads/")
			locals[b.Name] = true
		case strings.HasPrefix(fields[0], "refs/remotes/"):
			b.Name = strings.TrimPrefix(fields[0], "refs/remotes/")
			b.Remote = true
		default:
			continue
		}
		if fields[1] != "" {
			b.Upstream = strings.TrimPrefix(strings.TrimPrefix(fields[1], "refs/remotes/"), "refs/heads/")
			upstreams[fields[1]] = true
		}
		if fields[2] != "" {
			var err error
			b.CommitTime, err = strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				return result, err
			}
		}
		all = append(all, b)
	}
	for _, b := range all {
		if b.Remote {
			_, short, _ := strings.Cut(b.Name, "/")
			if locals[short] || upstreams["refs/remotes/"+b.Name] {
				continue
			}
		}
		if len(result.Branches) == MaxGitBranches {
			result.Truncated = true
			continue
		}
		result.Branches = append(result.Branches, b)
	}
	return result, nil
}

func GitSwitch(ctx context.Context, root string, req GitSwitchRequest) error {
	if len(req.DraftPaths) > 0 {
		return errors.New("cannot switch branches: save or discard unsaved drafts first")
	}
	if req.Name == "" || strings.HasPrefix(req.Name, "-") || strings.ContainsRune(req.Name, 0) || req.Create && req.Remote {
		return errors.New("invalid branch name or switch options")
	}
	return withGitWrite(ctx, root, func(w gitWriter) error {
		// --branch rejects @{-1} expansion only after expansion, so validate the full
		// literal ref first as well. Never accept revisions/options as branch names.
		prefix := "refs/heads/"
		if req.Remote {
			prefix = "refs/remotes/"
		}
		if _, err := w.run("", "check-ref-format", prefix+req.Name); err != nil {
			return fmt.Errorf("invalid branch name: %w", err)
		}
		if req.Create {
			if _, err := w.run("", "check-ref-format", "--branch", req.Name); err != nil {
				return err
			}
			_, err := w.run("", "switch", "--no-guess", "-c", req.Name)
			return err
		}
		if _, err := w.run("", "show-ref", "--verify", "--", prefix+req.Name); err != nil {
			return fmt.Errorf("branch does not exist: %w", err)
		}
		if req.Remote {
			// Explicit full ref avoids checkout.defaultRemote / DWIM ambiguity. Derive
			// and validate the local branch, rather than letting Git infer its name.
			_, local, ok := strings.Cut(req.Name, "/")
			if !ok || local == "" || strings.HasPrefix(local, "-") {
				return errors.New("invalid remote branch name")
			}
			if _, err := w.run("", "check-ref-format", "--branch", local); err != nil {
				return err
			}
			_, err := w.run("", "switch", "--no-guess", "--track", "-c", local, prefix+req.Name)
			return err
		}
		_, err := w.run("", "switch", "--no-guess", "--", req.Name)
		return err
	})
}
