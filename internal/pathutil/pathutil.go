// Package pathutil normalizes directories and locates Git worktree and main-repository roots.
package pathutil

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SQLiteURI returns a file: URI for the database at path with params as its
// query. An absolute path always gets a leading slash, so a Windows drive
// letter is not parsed as the URI authority: C:\x\db becomes file:///C:/x/db.
func SQLiteURI(path string, params url.Values) string {
	p := filepath.ToSlash(path)
	if filepath.IsAbs(path) && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: params.Encode()}).String()
}

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
