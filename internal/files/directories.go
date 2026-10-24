package files

import (
	"context"
	"errors"
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
// count and their check-ignore stdin size ("./", path, NUL). One oversized path
// is still allowed.
type frontierLimit struct{ paths, bytes int }

// directoryFrontier keeps one check-ignore's stdin, its echoed stdout and the
// candidate slice to a few hundred KiB; walks rarely reach it.
var directoryFrontier = frontierLimit{paths: 4096, bytes: 256 << 10}

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
				if len(frontier) > 0 && (len(frontier) >= limit.paths || frontierBytes+len(candidate)+3 > limit.bytes) {
					if decide(); truncated {
						break
					}
				}
				frontier = append(frontier, candidate)
				frontierBytes += len(candidate) + 3
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

// ignoredDirectories decides one frontier. check-ignore rejects
// --literal-pathspecs, so every path is anchored as "./path": a name starting
// with ':' is then a directory, not pathspec magic that could abort the batch
// (":!x") or check a different path (":x"). Only anchored echoes are answers.
func ignoredDirectories(ctx context.Context, root string, paths []string) (map[string]bool, bool) {
	var in strings.Builder
	for _, path := range paths {
		in.WriteString("./")
		in.WriteString(path)
		in.WriteByte(0)
	}
	out, err := gitOutput(ctx, root, strings.NewReader(in.String()), nil, "check-ignore", "-z", "--stdin")
	// Exit status 1 means that no path is ignored.
	if exit := (*exec.ExitError)(nil); err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil) {
		return nil, false
	}
	ignored := make(map[string]bool)
	for path := range strings.SplitSeq(string(out), "\x00") {
		if rel, ok := strings.CutPrefix(path, "./"); ok {
			ignored[rel] = true
		}
	}
	return ignored, true
}
