package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// AddFile copies a host-local source into a project-relative missing leaf.
func (e *Engine) AddFile(ctx context.Context, dir, path, source string) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	f, err := files.OpenAddSource(source)
	if err != nil {
		return err
	}
	defer f.Close()
	return files.Add(ctx, e.editPolicy(), dir, path, f)
}

var uploadToken = regexp.MustCompile(`^upload-[a-zA-Z0-9_-]{1,100}$`)

// CommitFileUpload consumes a private staged token, including on authorization
// or copy failure. No arbitrary server source path is accepted over RPC.
func (e *Engine) CommitFileUpload(ctx context.Context, dir, path, token string) error {
	if !uploadToken.MatchString(token) {
		return fmt.Errorf("invalid upload token")
	}
	cache := e.roots.Cache
	if e.cacheDirOverride != "" {
		cache = e.cacheDirOverride
	}
	if cache == "" {
		roots, err := paths.Default()
		if err != nil {
			return err
		}
		cache = roots.Cache
	}
	r, err := os.OpenRoot(filepath.Join(cache, "staging"))
	if err != nil {
		return err
	}
	defer r.Close()
	defer r.Remove(token)
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	before, err := r.Lstat(token)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("upload must be a regular file")
	}
	f, err := r.Open(token)
	if err != nil {
		return err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return fmt.Errorf("upload changed while opening")
	}
	return files.Add(ctx, e.editPolicy(), dir, path, f)
}
