package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CreateDirectory creates one missing directory beneath an existing project
// directory. An empty rel selects the project root. Like saves and renames it
// holds the save lock, rejects links below the root, and never replaces entries.
func CreateDirectory(ctx context.Context, policy EditPolicy, root, rel, name string) error {
	return createEntry(ctx, policy, root, rel, name, true)
}

// CreateFile creates one missing empty file on the same terms as
// CreateDirectory.
func CreateFile(ctx context.Context, policy EditPolicy, root, rel, name string) error {
	return createEntry(ctx, policy, root, rel, name, false)
}

func createEntry(ctx context.Context, policy EditPolicy, root, rel, name string, directory bool) error {
	unlock, err := lockSaves(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || !filepath.IsLocal(name) || filepath.Base(name) != name {
		if directory {
			return fmt.Errorf("create directory requires a basename")
		}
		return fmt.Errorf("create file requires a basename")
	}
	// Validate before joining: Join would erase traversal or an absolute parent.
	if rel != "" && (editHasParentComponent(rel) || !filepath.IsLocal(rel)) {
		return ErrOutsideRoot
	}
	if runtime.GOOS == "windows" {
		for _, component := range strings.FieldsFunc(rel+"/"+name, func(r rune) bool { return r == '/' || r == '\\' }) {
			if strings.ContainsAny(component, ":~") || (component != "." && strings.TrimRight(component, " .") != component) {
				return ErrOutsideRoot
			}
		}
	}
	target := filepath.Join(rel, name)
	if err := policy.checkTarget(root, target, true, directory); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if err := policy.checkTarget(real, target, true, directory); err != nil {
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
	parts := strings.Split(filepath.Clean(target), string(filepath.Separator))
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
	if err := policy.checkTarget(root, target, true, directory); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Mkdir, not MkdirAll, and exclusive creation atomically refuse any
	// existing leaf, including links.
	if directory {
		return parent.Mkdir(name, 0700)
	}
	f, err := parent.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}
