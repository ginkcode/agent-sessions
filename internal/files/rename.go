package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Rename changes only the basename of a regular file or directory. It shares
// the save lock so an editor cannot save through a name being renamed. External
// writers are not locked; the final OS operation atomically refuses replacement.
func Rename(ctx context.Context, policy EditPolicy, root, rel, name string) error {
	unlock, err := lockSaves(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// Reject both separator forms even on Unix, keeping the API basename-only.
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || !filepath.IsLocal(name) || filepath.Base(name) != name {
		return fmt.Errorf("rename requires a basename")
	}
	if runtime.GOOS == "windows" {
		// Reject ADS and Win32 normalization aliases of protected names.
		for _, component := range strings.FieldsFunc(rel+"/"+name, func(r rune) bool { return r == '/' || r == '\\' }) {
			if strings.Contains(component, ":") || (component != "." && strings.TrimRight(component, " .") != component) {
				return ErrOutsideRoot
			}
		}
	}
	if err := policy.checkTarget(root, rel, false, true); err != nil {
		return err
	}
	target := filepath.Join(filepath.Dir(rel), name)
	if err := policy.checkRenameDestination(root, rel, target); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if err := policy.checkTarget(real, rel, false, true); err != nil {
		return err
	}
	if err := policy.checkRenameDestination(real, rel, target); err != nil {
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
	source := parts[len(parts)-1]
	before, err := parent.Lstat(source)
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
		// Moving a directory also moves everything beneath it. Protect metadata and
		// credentials there too, without following links or reading file bodies.
		err := fs.WalkDir(parent.FS(), source, func(p string, d fs.DirEntry, walkErr error) error {
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
			return nil
		})
		if err != nil {
			return err
		}
	}
	// Recheck policy and identity immediately before the rooted syscall. As with
	// saves, malicious ongoing external source/ancestor swaps cannot be excluded.
	if err := policy.checkTarget(root, rel, false, true); err != nil {
		return err
	}
	if err := policy.checkRenameDestination(root, rel, target); err != nil {
		return err
	}
	after, err := parent.Lstat(source)
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() {
		return ErrEditTarget
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := renameNoReplace(dir, source, name); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Case-only renames may resolve to the source on case-insensitive volumes.
// Authorize that spelling only; the native no-replace operation remains decisive.
func (p EditPolicy) checkRenameDestination(root, source, target string) error {
	err := p.checkTarget(root, target, true, true)
	// Callers may supply slash-separated paths even on Windows. Normalize
	// before distinguishing a no-op from an actual case-only rename.
	source, target = filepath.Clean(source), filepath.Clean(target)
	if !errors.Is(err, os.ErrExist) || (runtime.GOOS != "darwin" && runtime.GOOS != "windows") || source == target || !strings.EqualFold(source, target) {
		return err
	}
	if err := p.checkTarget(root, target, false, true); err != nil {
		return err
	}
	a, aerr := os.Lstat(filepath.Join(root, source))
	b, berr := os.Lstat(filepath.Join(root, target))
	if aerr == nil && berr == nil && os.SameFile(a, b) {
		return nil
	}
	return os.ErrExist
}
