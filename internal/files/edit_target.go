package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// editTarget is a bounded raw-byte snapshot with handles to the file and its
// original parent directory. It does not authorize a save or enable eligibility.
type editTarget struct {
	// root is the canonical project root the target was opened under.
	root     string
	parent   *os.Root
	file     *os.File
	info     os.FileInfo
	name     string
	data     []byte
	format   EditInfo
	revision string
}

// Close releases both handles, including when closing one of them fails.
func (t *editTarget) Close() error {
	if t == nil {
		return nil
	}
	var fileErr, parentErr error
	if t.file != nil {
		fileErr = t.file.Close()
		t.file = nil
	}
	if t.parent != nil {
		parentErr = t.parent.Close()
		t.parent = nil
	}
	return errors.Join(fileErr, parentErr)
}

// openEditTarget repeats the metadata preflight through rooted handles and
// verifies identities around a bounded read. An opened parent isolates the
// original directory if its pathname is replaced. Preflight and these checks
// do NOT fully prevent ongoing external swaps or concurrent in-place writes;
// any future replacement must independently revalidate its target. Hard links
// and platform ownership/ACL/extended-attribute policy are separate checks.
func openEditTarget(ctx context.Context, policy EditPolicy, root, rel string) (*editTarget, error) {
	return openEditTargetFlag(ctx, policy, root, rel, os.O_RDONLY)
}

// sameDir reports an error unless r, opened from path, is the directory path
// still names: the path was checked by name, and the handle is what is used.
func sameDir(r *os.Root, path string) error {
	named, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: recheck project root: %w", ErrEditTarget, err)
	}
	dir, err := r.Open(".")
	if err != nil {
		return fmt.Errorf("%w: inspect project root: %w", ErrEditTarget, err)
	}
	opened, statErr := dir.Stat()
	closeErr := dir.Close()
	if statErr != nil || closeErr != nil {
		return fmt.Errorf("%w: inspect project root: %w", ErrEditTarget, errors.Join(statErr, closeErr))
	}
	if !named.IsDir() || !os.SameFile(named, opened) {
		return fmt.Errorf("%w: project root changed", ErrEditTarget)
	}
	return nil
}

// openEditTargetFlag is openEditTarget with the target opened using flag
// (os.O_RDONLY or os.O_RDWR); it never creates or truncates the file.
func openEditTargetFlag(ctx context.Context, policy EditPolicy, root, rel string, flag int) (*editTarget, error) {
	if flag != os.O_RDONLY && flag != os.O_RDWR {
		return nil, fmt.Errorf("%w: unsupported open mode", ErrEditTarget)
	}
	if err := policy.Check(root, rel); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize project root: %w", ErrEditTarget, err)
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: absolute project root: %w", ErrEditTarget, err)
	}
	// root may be a symlink resolved again since the check above: check the
	// policy against the root actually opened too.
	if err := policy.Check(realRoot, rel); err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(realRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: open project root: %w", ErrEditTarget, err)
	}
	t := &editTarget{root: realRoot, parent: parent}
	ok := false
	defer func() {
		if !ok {
			t.Close()
		}
	}()
	if err := sameDir(parent, realRoot); err != nil {
		return nil, err
	}

	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for _, name := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := t.parent.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("%w: parent %s: %w", ErrEditTarget, name, err)
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil, fmt.Errorf("%w: parent %s", ErrEditLink, name)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: parent %s", ErrEditTarget, name)
		}
		next, err := t.parent.OpenRoot(name)
		if err != nil {
			return nil, fmt.Errorf("%w: open parent %s: %w", ErrEditTarget, name, err)
		}
		// Root.Stat follows links; inspect the actual opened directory instead.
		dir, err := next.Open(".")
		if err != nil {
			next.Close()
			return nil, fmt.Errorf("%w: inspect parent %s: %w", ErrEditTarget, name, err)
		}
		opened, statErr := dir.Stat()
		closeErr := dir.Close()
		if statErr != nil || closeErr != nil {
			next.Close()
			return nil, fmt.Errorf("%w: inspect parent %s: %w", ErrEditTarget, name, errors.Join(statErr, closeErr))
		}
		if !opened.IsDir() || !os.SameFile(info, opened) {
			next.Close()
			return nil, fmt.Errorf("%w: parent %s changed", ErrEditTarget, name)
		}
		old := t.parent
		t.parent = next
		if err := old.Close(); err != nil {
			return nil, fmt.Errorf("%w: close ancestor: %w", ErrEditTarget, err)
		}
	}

	t.name = parts[len(parts)-1]
	before, err := t.parent.Lstat(t.name)
	if err != nil {
		return nil, fmt.Errorf("%w: target: %w", ErrEditTarget, err)
	}
	if before.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, ErrEditLink
	}
	if !before.Mode().IsRegular() {
		return nil, ErrEditTarget
	}
	t.file, err = t.parent.OpenFile(t.name, flag, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("%w: open target", ErrEditReadOnly)
		}
		return nil, fmt.Errorf("%w: open target: %w", ErrEditTarget, err)
	}
	t.info, err = t.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: stat target: %w", ErrEditTarget, err)
	}
	if !t.info.Mode().IsRegular() || !os.SameFile(before, t.info) || before.Size() != t.info.Size() || !before.ModTime().Equal(t.info.ModTime()) {
		return nil, fmt.Errorf("%w: target changed while opening", ErrEditTarget)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.data, err = io.ReadAll(io.LimitReader(t.file, MaxEditBytes+1))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read target: %w", ErrEditTarget, err)
	}
	if len(t.data) > MaxEditBytes {
		return nil, ErrEditTooLarge
	}

	after, err := t.parent.Lstat(t.name)
	if err != nil {
		return nil, fmt.Errorf("%w: recheck target: %w", ErrEditTarget, err)
	}
	opened, err := t.file.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: recheck opened target: %w", ErrEditTarget, err)
	}
	for _, info := range []os.FileInfo{after, opened} {
		if !info.Mode().IsRegular() || !os.SameFile(t.info, info) || info.Size() != t.info.Size() || !info.ModTime().Equal(t.info.ModTime()) {
			return nil, fmt.Errorf("%w: target changed while reading", ErrEditTarget)
		}
	}
	if int64(len(t.data)) != t.info.Size() {
		return nil, fmt.Errorf("%w: target size changed while reading", ErrEditTarget)
	}
	if IsBinary(t.data) {
		return nil, ErrUnsupportedEditText
	}
	t.format = *textEditInfo(t.data)
	if t.format.Reason == EditUnsupportedNewlines {
		return nil, ErrUnsupportedEditText
	}
	// not_checked describes supported format, not snapshot rejection.
	t.revision = revisionOf(t.data)
	ok = true
	return t, nil
}
