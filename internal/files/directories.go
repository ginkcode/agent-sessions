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

// gitDirectories visits directory entries in bounded batches. check-ignore is
// applied before descending, so ignored dependency trees are never enumerated.
// Neither symlink directories nor repository metadata are followed.
func gitDirectories(ctx context.Context, root string) ([]string, bool, error) {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	var directories []string
	pending := []string{""}
	visited := 0
	truncated := false
	for len(pending) > 0 {
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
		for {
			entries, readErr := f.ReadDir(128)
			var candidates []string
			for _, entry := range entries {
				visited++
				if visited > MaxFiles*4 {
					truncated = true
					break
				}
				if entry.IsDir() && entry.Name() != ".git" {
					candidates = append(candidates, filepath.ToSlash(filepath.Join(rel, entry.Name())))
				}
			}
			if len(candidates) > 0 {
				ignored, ok := ignoredDirectories(ctx, root, candidates)
				if !ok {
					// Fail closed: never walk possibly ignored trees after git fails.
					truncated = true
				} else {
					for _, candidate := range candidates {
						if ignored[candidate] {
							continue
						}
						if len(directories) >= MaxFiles {
							truncated = true
							break
						}
						directories = append(directories, candidate)
						pending = append(pending, candidate)
					}
				}
			}
			if truncated || readErr != nil {
				break
			}
		}
		f.Close()
		if truncated {
			break
		}
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
