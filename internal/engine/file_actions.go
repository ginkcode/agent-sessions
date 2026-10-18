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

// ResolveFile authorizes a native file action using the same catalog as ReadFile.
func (e *Engine) ResolveFile(ctx context.Context, dir, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !e.svc.knownDirectory(dir) {
		return "", fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	full, err := files.Resolve(dir, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a file or directory: %s", path)
	}
	return full, nil
}

var downloadToken = regexp.MustCompile(`^download-[a-zA-Z0-9_-]{1,100}$`)

// PrepareFileDownload writes only to a token in our private staging directory;
// callers cannot select an arbitrary server destination through this RPC.
func (e *Engine) PrepareFileDownload(ctx context.Context, dir, path, token string) (files.Download, error) {
	if !downloadToken.MatchString(token) {
		return files.Download{}, fmt.Errorf("invalid download token")
	}
	if _, err := e.ResolveFile(ctx, dir, path); err != nil {
		return files.Download{}, err
	}
	cache := e.roots.Cache
	if e.cacheDirOverride != "" {
		cache = e.cacheDirOverride
	}
	if cache == "" {
		roots, err := paths.Default()
		if err != nil {
			return files.Download{}, err
		}
		cache = roots.Cache
	}
	staging := filepath.Join(cache, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return files.Download{}, err
	}
	// OpenRoot also protects staging against token symlinks; O_EXCL prevents
	// replacing an existing transfer or following a pre-created symlink.
	r, err := os.OpenRoot(staging)
	if err != nil {
		return files.Download{}, err
	}
	defer r.Close()
	f, err := r.OpenFile(token, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return files.Download{}, err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = r.Remove(token)
		}
	}()
	out, err := files.WriteDownload(ctx, dir, path, f)
	if err != nil {
		return files.Download{}, err
	}
	if err := f.Close(); err != nil {
		return files.Download{}, err
	}
	ok = true
	out.Token = token
	return out, nil
}
