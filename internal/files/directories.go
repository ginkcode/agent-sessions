package files

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ignoreCheck reports which candidate paths are ignored; false means unknown.
type ignoreCheck func(ctx context.Context, root string, paths []string) (map[string]bool, bool)

// frontierLimit bounds the candidates awaiting one ignore decision: a path
// count and their NUL-terminated stdin size. One oversized path is still allowed.
type frontierLimit struct{ paths, bytes int }

var directoryFrontier = frontierLimit{paths: 128, bytes: 64 << 10}

// gitDirectories visits directory entries in bounded batches. check-ignore is
// applied before descending, so ignored dependency trees are never enumerated.
// Neither symlink directories nor repository metadata are followed.
func gitDirectories(ctx context.Context, root string) ([]string, bool, error) {
	return walkDirectories(ctx, root, ignoredDirectories, directoryFrontier)
}

// walkDirectories collects candidate subdirectories from as many unvisited
// directories as fit in one frontier, decides them together, and only then
// queues the survivors. Reads stay chunked; a full frontier is decided mid-read.
func walkDirectories(ctx context.Context, root string, check ignoreCheck, limit frontierLimit) ([]string, bool, error) {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	var directories, frontier []string
	pending := []string{""}
	visited, frontierBytes := 0, 0
	truncated, capped := false, false
	decide := func() {
		batch := frontier
		frontier, frontierBytes = nil, 0
		if len(batch) == 0 {
			return
		}
		ignored, ok := check(ctx, root, batch)
		if !ok {
			// Fail closed: never walk possibly ignored trees after git fails.
			truncated = true
			return
		}
		for _, candidate := range batch {
			if ignored[candidate] {
				continue
			}
			if len(directories) >= MaxFiles {
				truncated = true
				return
			}
			directories = append(directories, candidate)
			pending = append(pending, candidate)
		}
	}
	for !truncated {
		if len(pending) == 0 {
			if len(frontier) == 0 {
				break
			}
			decide()
			continue
		}
		if ctx.Err() != nil {
			if err := callerCtx.Err(); err != nil {
				return nil, false, err
			}
			truncated = true
			break
		}
		rel := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		path := filepath.FromSlash(rel)
		if path == "" {
			path = "."
		}
		f, err := r.Open(path)
		if err != nil {
			continue
		}
		for !truncated {
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				visited++
				if visited > MaxFiles*4 {
					truncated, capped = true, true
					break
				}
				if !entry.IsDir() || entry.Name() == ".git" {
					continue
				}
				candidate := filepath.ToSlash(filepath.Join(rel, entry.Name()))
				if len(frontier) > 0 && (len(frontier) >= limit.paths || frontierBytes+len(candidate)+1 > limit.bytes) {
					if decide(); truncated {
						break
					}
				}
				frontier = append(frontier, candidate)
				frontierBytes += len(candidate) + 1
			}
			if readErr != nil {
				break
			}
		}
		f.Close()
	}
	if capped {
		// Entries seen before the visit cap are still listed, never descended.
		decide()
	}
	if err := callerCtx.Err(); err != nil {
		return nil, false, err
	}
	slices.Sort(directories)
	return directories, truncated, nil
}

func ignoredDirectories(ctx context.Context, root string, paths []string) (map[string]bool, bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-z", "--stdin")
	hideConsole(cmd)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	countGitInvocation(ctx, "check-ignore")
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return nil, false
		}
	}
	ignored := make(map[string]bool)
	for path := range strings.SplitSeq(stdout.String(), "\x00") {
		ignored[path] = true
	}
	return ignored, true
}
