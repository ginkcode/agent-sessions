package files

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Delete permanently removes a regular file or directory, serialized with saves.
// Confirmation belongs to the caller. No file bodies are read, and deletion is
// rooted so that links cannot redirect recursive removal outside the project.
// Recursive deletion is not atomic: an error can leave some contents removed.
func Delete(ctx context.Context, policy EditPolicy, root, rel string) error {
	unlock, err := lockSaves(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if runtime.GOOS == "windows" {
		// Reject ADS and Win32 normalization aliases of protected names.
		for _, component := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
			if strings.ContainsAny(component, ":~") || (component != "." && strings.TrimRight(component, " .") != component) {
				return ErrOutsideRoot
			}
		}
	}
	if err := policy.checkTarget(root, rel, false, true); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if err := policy.checkTarget(real, rel, false, true); err != nil {
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
	for _, component := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := parent.Lstat(component)
		if err != nil {
			return err
		}
		if !before.IsDir() || before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return ErrEditLink
		}
		next, err := parent.OpenRoot(component)
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
	name := parts[len(parts)-1]
	before, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return ErrEditLink
	}
	if !before.IsDir() && !before.Mode().IsRegular() {
		return ErrEditTarget
	}
	if before.IsDir() {
		// Preflight the entire subtree before removing anything. Fail closed for
		// metadata, credentials, links and special entries, just as rename does.
		err := fs.WalkDir(parent.FS(), name, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if editProtectedName(d.Name()) {
				return fmt.Errorf("%w: nested %s", ErrEditProtected, p)
			}
			if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				return ErrEditLink
			}
			if !d.IsDir() && !d.Type().IsRegular() {
				return ErrEditTarget
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	// As with saves/renames, ongoing malicious external swaps cannot be fully
	// excluded. Recheck policy and identity immediately before rooted removal;
	// Root.RemoveAll itself does not follow symlinks encountered during removal.
	if err := policy.checkTarget(root, rel, false, true); err != nil {
		return err
	}
	if err := policy.checkTarget(real, rel, false, true); err != nil {
		return err
	}
	after, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() {
		return ErrEditTarget
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.RemoveAll(name); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
