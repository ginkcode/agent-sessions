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

// NormalizeDir cleans p in its own style (see Clean) and resolves symlinks
// when p exists on this machine. A filesystem root is preserved; other
// trailing separators are removed.
func NormalizeDir(p string) string {
	if p == "" {
		return ""
	}
	p = Clean(p)
	if isForeign(p) {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = Clean(resolved)
	}
	return p
}

// Exists reports whether p names an existing file or directory. Broken
// symlinks do not count as existing, and neither do absolute paths in the
// other OS's style.
func Exists(p string) bool {
	if isForeign(p) {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
