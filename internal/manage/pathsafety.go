package manage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathOutsideRoot is returned when a target is not strictly inside its
// configured provider root, including through an ancestor symlink.
var ErrPathOutsideRoot = errors.New("path is outside the provider root")

func isProtectedName(name string) bool {
	switch name {
	case "auth.json", "history.jsonl", "file-history", "session-env", "storage":
		return true
	}
	return strings.HasPrefix(name, ".credentials") || strings.HasPrefix(name, "opencode.db") || (strings.HasPrefix(name, "state_") && strings.HasSuffix(name, ".sqlite"))
}

type pathSafety struct{ root string }

func newPathSafety(root string) (*pathSafety, error) {
	if root == "" {
		return nil, errors.New("manage: empty provider root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.New("manage: invalid provider root")
	}
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return nil, ErrPathOutsideRoot
	}
	return &pathSafety{root: abs}, nil
}

func (ps *pathSafety) resolvedRoot() (string, error) {
	fi, err := os.Lstat(ps.root)
	if err != nil {
		return "", errors.New("manage: provider root is unavailable")
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("manage: unsafe provider root")
	}
	root, err := filepath.EvalSymlinks(ps.root)
	if err != nil {
		return "", errors.New("manage: provider root is unavailable")
	}
	if root == string(filepath.Separator) {
		return "", ErrPathOutsideRoot
	}
	return root, nil
}

func contained(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ensureWithinRoot checks that a path that may not exist yet will land inside
// the provider root. Existing ancestors are walked and refused when they are
// symlinks, protected names, or escape the resolved root. Missing components
// are checked lexically only, since restore creates them.
func (ps *pathSafety) ensureWithinRoot(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return ErrPathOutsideRoot
	}
	root, err := ps.resolvedRoot()
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	if !contained(ps.root, path) {
		return ErrPathOutsideRoot
	}
	rel, err := filepath.Rel(ps.root, path)
	if err != nil {
		return ErrPathOutsideRoot
	}
	current := ps.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if isProtectedName(part) {
			return errors.New("manage: protected path refused")
		}
		current = filepath.Join(current, part)
		stat, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				// Nothing below exists yet. The lexical check above is enough.
				return nil
			}
			return errors.New("manage: target is unavailable")
		}
		if stat.Mode()&os.ModeSymlink != 0 {
			return errors.New("manage: symlink path refused")
		}
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return errors.New("manage: target parent is unavailable")
	}
	if !contained(root, filepath.Join(parent, filepath.Base(path))) {
		return ErrPathOutsideRoot
	}
	return nil
}

func (ps *pathSafety) classify(path string) (os.FileMode, error) {
	if path == "" || !filepath.IsAbs(path) {
		return 0, ErrPathOutsideRoot
	}
	root, err := ps.resolvedRoot()
	if err != nil {
		return 0, err
	}
	path = filepath.Clean(path)
	// Require lexical containment as well: a symlinked ancestor must not
	// let a caller name an unrelated target inside the resolved directory.
	if !contained(ps.root, path) {
		return 0, ErrPathOutsideRoot
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, errors.New("manage: target is unavailable")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("manage: symlink target refused")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return 0, errors.New("manage: target parent is unavailable")
	}
	if !contained(root, filepath.Join(parent, filepath.Base(path))) {
		return 0, ErrPathOutsideRoot
	}
	// Reject symlinked ancestors too: aliases can race and could move files
	// outside the physical layout a provider expects.
	rel, _ := filepath.Rel(ps.root, path)
	current := ps.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if isProtectedName(part) {
			return 0, errors.New("manage: protected path refused")
		}
		current = filepath.Join(current, part)
		stat, err := os.Lstat(current)
		if err != nil {
			return 0, errors.New("manage: target is unavailable")
		}
		if stat.Mode()&os.ModeSymlink != 0 {
			return 0, errors.New("manage: symlink path refused")
		}
	}
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return 0, errors.New("manage: non-regular target refused")
	}
	return fi.Mode(), nil
}

// checkTree refuses a whole directory if any nested item is protected,
// symlinked, unreadable, or special. This is mandatory before trashing a
// Claude session directory (the trash tool moves its contents recursively).
func (ps *pathSafety) checkTree(ctxDone func() error, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctxDone(); err != nil {
			return err
		}
		if walkErr != nil {
			return errors.New("manage: cannot inspect session directory")
		}
		if isProtectedName(d.Name()) {
			return errors.New("manage: protected entry inside session directory")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("manage: symlink inside session directory")
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return errors.New("manage: special entry inside session directory")
		}
		if _, err := ps.classify(path); err != nil {
			return err
		}
		return nil
	})
}

type itemFile struct {
	Path string
	Mode os.FileMode
}

func bytesOf(path string, mode os.FileMode) int64 {
	if !mode.IsDir() {
		fi, err := os.Lstat(path)
		if err == nil {
			return fi.Size()
		}
		return 0
	}
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil && fi.Mode().IsRegular() {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

func filesForPaths(ps *pathSafety, paths []string) ([]itemFile, error) {
	files := make([]itemFile, 0, len(paths))
	for _, p := range paths {
		mode, err := ps.classify(p)
		if err != nil {
			return nil, err
		}
		files = append(files, itemFile{Path: p, Mode: mode})
	}
	return files, nil
}

func safePathError(err error) error {
	if errors.Is(err, ErrPathOutsideRoot) {
		return ErrPathOutsideRoot
	}
	return fmt.Errorf("manage: unsafe target: %w", err)
}
