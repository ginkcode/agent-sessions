package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// ResolveTerminalFile resolves a terminal path on this backend's host. The
// supplied terminal cwd is only a base for relative paths, never an authority:
// only cwd/repo roots already in the catalog can authorize the resulting file.
func (e *Engine) ResolveTerminalFile(ctx context.Context, dir, path string) (files.TerminalFile, error) {
	if err := ctx.Err(); err != nil {
		return files.TerminalFile{}, err
	}
	if path == "" {
		return files.TerminalFile{}, fmt.Errorf("empty terminal path")
	}
	target := path
	if !filepath.IsAbs(target) {
		// Reject Windows drive-relative and rooted-but-not-absolute paths;
		// neither has an unambiguous meaning relative to the terminal cwd.
		if !filepath.IsAbs(dir) || filepath.VolumeName(target) != "" || os.IsPathSeparator(target[0]) {
			return files.TerminalFile{}, fmt.Errorf("terminal path requires an absolute cwd")
		}
		target = filepath.Join(dir, target)
	}
	target = filepath.Clean(target)

	var root, rel string
	for _, meta := range e.svc.catalog.All() {
		for _, candidate := range []string{meta.CWD, meta.RepoRoot} {
			if !filepath.IsAbs(candidate) {
				continue
			}
			candidate = filepath.Clean(candidate)
			r, err := filepath.Rel(candidate, target)
			if err != nil || !filepath.IsLocal(r) {
				continue
			}
			if len(candidate) > len(root) {
				root, rel = candidate, r
			}
		}
	}
	if root == "" {
		return files.TerminalFile{}, fmt.Errorf("%w: %s", ErrUnknownDirectory, target)
	}
	// Do not retry a broader root on failure: the most specific catalog root
	// is the security boundary, including for symlinks into sibling projects.
	full, err := files.Resolve(root, rel)
	if err != nil {
		return files.TerminalFile{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return files.TerminalFile{}, err
	}
	if !info.Mode().IsRegular() {
		return files.TerminalFile{}, fmt.Errorf("not a regular file: %s", path)
	}
	return files.TerminalFile{Dir: root, Path: filepath.ToSlash(rel)}, nil
}
