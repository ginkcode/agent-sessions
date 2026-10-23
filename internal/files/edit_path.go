package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrEditProtected = errors.New("edit target is protected")
	ErrEditLink      = errors.New("edit path contains a link or irregular entry")
	ErrEditTarget    = errors.New("edit target must be an existing regular file with directory parents")
)

// EditPolicy is the host-local protection policy for project editing. Callers
// must supply all configured tool store roots, even when they are outside the
// usual home directory. ProtectedRoots must be absolute without '..' components;
// nonexistent roots are allowed, but errors resolving their existing ancestors
// fail closed.
// The zero value still protects credential and repository metadata names.
type EditPolicy struct {
	ProtectedRoots []string
}

// Check performs a metadata-only preflight; it neither reads file bodies nor
// enables editing. root may itself be a symlink. Below it every parent must be
// a directory and the existing target must be regular, with no symlink or
// irregular component (including Windows junctions). All input paths reject
// any '..' component before normalization: cleaning it could hide a symlink,
// missing entry, or non-directory parent and change filesystem semantics.
// Single-dot components ('./') are allowed.
//
// This is NOT a race-safe authorization for a later pathname-based write:
// components can change between checks. A save must use rooted opens, recheck
// identity and eligibility, and implement platform-safe replacement separately.
func (p EditPolicy) Check(root, rel string) error {
	return p.check(root, rel, false)
}

// check also supports creation of a missing leaf, with identical protection rules.
func (p EditPolicy) check(root, rel string, create bool) error {
	return p.checkTarget(root, rel, create, false)
}

// checkTarget permits directory leaves for rename/delete; editing still requires regular files.
func (p EditPolicy) checkTarget(root, rel string, create, directory bool) error {
	if editHasParentComponent(rel) || !filepath.IsLocal(rel) || filepath.Clean(rel) == "." {
		return ErrOutsideRoot
	}
	if editHasParentComponent(root) {
		return fmt.Errorf("%w: project root contains '..'", ErrEditTarget)
	}
	lexRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("%w: project root: %w", ErrEditTarget, err)
	}
	realRoot, err := filepath.EvalSymlinks(lexRoot)
	if err != nil {
		return fmt.Errorf("%w: project root: %w", ErrEditTarget, err)
	}
	lexTarget := filepath.Join(lexRoot, rel)
	realTarget := filepath.Join(realRoot, rel)
	if err := p.refuse(directory, lexTarget, realTarget); err != nil {
		return err
	}
	r, err := os.OpenRoot(realRoot)
	if err != nil {
		return fmt.Errorf("%w: open project root: %w", ErrEditTarget, err)
	}
	defer r.Close()
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for i := range parts {
		name := filepath.Join(parts[:i+1]...)
		info, err := r.Lstat(name)
		if create && i == len(parts)-1 {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err == nil {
				return os.ErrExist
			}
		}
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrEditTarget, name, err)
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("%w: %s", ErrEditLink, name)
		}
		if (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular() && !(directory && info.IsDir())) {
			return fmt.Errorf("%w: %s", ErrEditTarget, name)
		}
	}
	return nil
}

// refuse fails when a target is in a configured store or has a protected
// name in any component. A directory target also fails when it holds a store.
func (p EditPolicy) refuse(directory bool, targets ...string) error {
	for _, protected := range p.ProtectedRoots {
		if !filepath.IsAbs(protected) || editHasParentComponent(protected) {
			return fmt.Errorf("%w: policy root must be absolute without '..' components: %q", ErrEditProtected, protected)
		}
		lexProtected := filepath.Clean(protected)
		realProtected, err := editResolveAncestor(lexProtected)
		if err != nil {
			return fmt.Errorf("%w: resolve policy root %q: %w", ErrEditProtected, protected, err)
		}
		for _, target := range targets {
			if editWithin(lexProtected, target) || editWithin(realProtected, target) ||
				(directory && (editWithin(target, lexProtected) || editWithin(target, realProtected))) {
				return fmt.Errorf("%w: configured store", ErrEditProtected)
			}
		}
	}
	for _, target := range targets {
		for _, component := range strings.Split(filepath.ToSlash(target), "/") {
			if editProtectedName(component) {
				return fmt.Errorf("%w: %s", ErrEditProtected, component)
			}
		}
	}
	return nil
}

// Use native separators (both slash forms on Windows), without cleaning first.
func editHasParentComponent(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || (runtime.GOOS == "windows" && r == '\\')
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func editProtectedName(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case ".git", ".claude", ".codex", ".ssh", ".aws", ".gnupg", ".azure", "auth.json":
		return true
	}
	return strings.HasPrefix(name, ".credentials") ||
		strings.HasPrefix(name, "opencode.db") ||
		(strings.HasPrefix(name, "state_") && strings.Contains(name, ".sqlite"))
}

// editWithin uses component boundaries, conservatively folding case on macOS
// and Windows. This can overblock on case-sensitive macOS volumes, intentionally.
func editWithin(root, target string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		root, target = strings.ToLower(root), strings.ToLower(target)
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// Resolve the longest existing ancestor, then append missing components. Lstat
// distinguishes missing entries from dangling links; a dangling link, a file
// used as a parent, permission errors, and all other failures are rejected.
func editResolveAncestor(path string) (string, error) {
	var missing []string
	for {
		info, err := os.Lstat(path)
		if err == nil {
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			if len(missing) > 0 {
				if info.Mode()&os.ModeSymlink != 0 {
					info, err = os.Stat(real)
					if err != nil {
						return "", err
					}
				}
				if !info.IsDir() {
					return "", ErrEditTarget
				}
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}
