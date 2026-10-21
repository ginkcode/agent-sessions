package files

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Add copies a regular file into a missing project leaf. All destination parents
// must already exist. Rooted directory handles and identity checks prevent link
// traversal; exclusive creation never overwrites an existing entry.
func Add(ctx context.Context, policy EditPolicy, root, rel string, src *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file")
	}
	if err := policy.check(root, rel, true); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if err := policy.check(real, rel, true); err != nil {
		return err
	}
	parent, err := os.OpenRoot(real)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if err := sameDir(parent, real); err != nil {
		return err
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for _, name := range parts[:len(parts)-1] {
		before, err := parent.Lstat(name)
		if err != nil {
			return err
		}
		if !before.IsDir() || before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return ErrEditLink
		}
		next, err := parent.OpenRoot(name)
		if err != nil {
			return err
		}
		f, err := next.Open(".")
		if err != nil {
			next.Close()
			return err
		}
		opened, statErr := f.Stat()
		f.Close()
		if statErr != nil || !os.SameFile(before, opened) {
			next.Close()
			return ErrEditLink
		}
		parent.Close()
		parent = next
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name := parts[len(parts)-1]
	out, err := parent.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			_ = parent.Remove(name)
		}
	}()
	if _, err := io.Copy(out, &addReader{ctx: ctx, r: src}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type addReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *addReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// OpenAddSource rejects links and non-regular sources before opening them.
func OpenAddSource(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("source must be a regular file, not a link")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		f.Close()
		return nil, fmt.Errorf("source changed while opening")
	}
	return f, nil
}
