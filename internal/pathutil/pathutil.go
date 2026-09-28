// Package pathutil normalizes directories and locates Git worktree and main-repository roots.
package pathutil

import (
	"os"
	"path/filepath"
)

// NormalizeDir cleans p and resolves symlinks when p exists. A filesystem root
// is preserved; other trailing separators are removed.
func NormalizeDir(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

// Exists reports whether p names an existing file or directory. Broken
// symlinks do not count as existing.
func Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
