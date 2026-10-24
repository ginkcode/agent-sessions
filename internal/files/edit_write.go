package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ErrEditReadOnly reports a target this user cannot write.
var ErrEditReadOnly = errors.New("file is read-only")

// ErrInvalidRevision reports a save without a well-formed expected revision.
var ErrInvalidRevision = errors.New("invalid expected revision")

// ErrSaveIncomplete reports a save that failed after writing began, so the
// file may hold the old bytes, the new ones or a mix. Clients must not treat
// the draft as either saved or unsaved until they read the file again. The
// text is matched by the frontend; keep it stable.
var ErrSaveIncomplete = errors.New("the save failed after writing began; the file may have changed")

var revisionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// saveSlot is one global lock, as a one-slot semaphore so a waiter can give
// up when its context ends. It serializes every save, rename, and editing
// check in this process, whatever path or project names the file: hard links
// and aliased roots name one file many ways, and saves are short and rare
// enough that per-file locking would only add hazards. Other processes are
// not excluded: see Save.
var saveSlot = make(chan struct{}, 1)

// lockSaves takes saveSlot, or returns ctx's error if ctx ends first. The
// returned function releases it.
func lockSaves(ctx context.Context) (func(), error) {
	select {
	case saveSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// select picks at random when both are ready.
	if err := ctx.Err(); err != nil {
		<-saveSlot
		return nil, err
	}
	return func() { <-saveSlot }, nil
}

// CheckEdit completes the edit eligibility of a text read: the path must
// pass the policy, and the file must be writable by this user. It changes
// only info.Eligible and info.Reason, and leaves a format refusal (too
// large, unsupported newlines) as it is.
func CheckEdit(policy EditPolicy, root, rel string, info *EditInfo) {
	if info == nil || info.Reason != EditNotChecked {
		return
	}
	info.Reason = editReason(policy.Check(root, rel))
	if info.Reason != "" {
		return
	}
	// The check reads the file; under the lock a save of this process
	// cannot be rewriting it meanwhile and make it look changed.
	unlock, err := lockSaves(context.Background())
	if err != nil {
		info.Reason = editReason(err)
		return
	}
	defer unlock()
	// Opening for writing is the one check every platform and file system
	// answers truthfully, ACLs and read-only attributes included. Nothing
	// is written.
	t, err := openEditTargetFlag(context.Background(), policy, root, rel, os.O_RDWR)
	if err != nil {
		info.Reason = editReason(err)
		return
	}
	_ = t.Close()
	info.Eligible = true
}

// editReason maps a check error to an EditInfo reason; "" for none.
func editReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrEditProtected):
		return EditProtected
	case errors.Is(err, ErrEditLink):
		return EditLinked
	case errors.Is(err, ErrEditReadOnly):
		return EditReadOnly
	case errors.Is(err, ErrEditTooLarge):
		return EditTooLarge
	case errors.Is(err, ErrUnsupportedEditText):
		return EditUnsupportedNewlines
	}
	return EditUnsupported
}

// Save writes an editor snapshot over the file rel names under root, if the
// file still has the expected revision. It returns a conflict result, and
// writes nothing, when the file has changed since it was read. A snapshot
// equal to the file's bytes is saved without writing.
//
// The file is rewritten in place, as editors such as VS Code do: it keeps
// its identity, so its permissions, owner, ACLs, extended attributes and
// hard links are kept with no copying. A save through any of the file's
// hard links therefore changes all of them, files outside the project
// included. The cost is that a crash or a full disk mid-write can leave the
// file cut short; the editor keeps the draft in that case.
//
// Saves in this process are serialized (see saveSlot), and the file is
// checked again just before it is written. Another process can still write
// the file between that check and the write, or while it is written, and
// Save does not claim to prevent it.
func Save(ctx context.Context, policy EditPolicy, root string, req SaveRequest) (SaveResult, error) {
	if !revisionPattern.MatchString(req.ExpectedRevision) {
		return SaveResult{}, ErrInvalidRevision
	}
	unlock, err := lockSaves(ctx)
	if err != nil {
		return SaveResult{}, err
	}
	res, err := saveLocked(ctx, policy, root, req)
	// Diffing against git does not touch the file; do it unlocked.
	unlock()
	if err == nil && res.Content != nil {
		if full, err := Resolve(root, res.Content.Path); err == nil {
			res.Content.Changes = gitChanges(ctx, full)
		}
	}
	return res, err
}

// saveLocked is Save with saveSlot held.
func saveLocked(ctx context.Context, policy EditPolicy, root string, req SaveRequest) (SaveResult, error) {
	// Opened under the lock, so no earlier save of this process is still
	// writing the file while it is read.
	t, err := openEditTargetFlag(ctx, policy, root, req.Path, os.O_RDWR)
	if err != nil {
		return SaveResult{}, err
	}
	defer t.Close()

	if t.revision != req.ExpectedRevision {
		return SaveResult{Status: SaveStatusConflict, ObservedRevision: t.revision}, nil
	}
	// The snapshot has a supported format: openEditTarget refuses others.
	data, err := encodeEditText(req.Text, t.format)
	if err != nil {
		return SaveResult{}, err
	}
	if !bytes.Equal(data, t.data) {
		if err := t.revalidate(policy, root, req.Path); err != nil {
			return SaveResult{}, err
		}
		// Last chance to stop: once the write starts it is finished.
		if err := ctx.Err(); err != nil {
			return SaveResult{}, err
		}
		if err := t.overwrite(data); err != nil {
			return SaveResult{}, err
		}
	}
	out := Content{
		Path:     t.relPath(req.Path),
		Size:     int64(len(data)),
		Text:     string(data),
		Revision: revisionOf(data),
		Edit:     textEditInfo(data),
	}
	// Still eligible: it was opened for writing and passed the policy.
	if out.Edit.Reason == EditNotChecked {
		out.Edit.Reason = ""
		out.Edit.Eligible = true
	}
	return SaveResult{Status: SaveStatusSaved, Content: &out}, nil
}

// relPath is the slash-separated path a Content reports, as Read gives it.
func (t *editTarget) relPath(rel string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
}

// revalidate checks, just before a write, that the open file is still the
// one the snapshot was read from and that rel under root still names it and
// still passes the policy: its parent may have been moved, or the name
// replaced, since it was opened. Only metadata is compared, so an external
// rewrite keeping the size and modification time goes unnoticed.
func (t *editTarget) revalidate(policy EditPolicy, root, rel string) error {
	opened, err := t.file.Stat()
	if err != nil {
		return fmt.Errorf("%w: recheck opened target: %w", ErrEditTarget, err)
	}
	if opened.Size() != int64(len(t.data)) || !opened.ModTime().Equal(t.info.ModTime()) {
		return fmt.Errorf("%w: target changed since it was read", ErrEditTarget)
	}
	named, err := t.parent.Lstat(t.name)
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(named, t.info) {
		return fmt.Errorf("%w: target was replaced", ErrEditTarget)
	}
	if err := policy.Check(root, rel); err != nil {
		return err
	}
	// root, if a link, must still lead to the project the file was opened in.
	realRoot, err := filepath.EvalSymlinks(root)
	if err == nil {
		realRoot, err = filepath.Abs(realRoot)
	}
	if err != nil || realRoot != t.root {
		return fmt.Errorf("%w: project root moved", ErrEditTarget)
	}
	r, err := os.OpenRoot(realRoot)
	if err != nil {
		return fmt.Errorf("%w: open project root: %w", ErrEditTarget, err)
	}
	defer r.Close()
	if err := sameDir(r, realRoot); err != nil {
		return err
	}
	current, err := r.Lstat(filepath.Clean(rel))
	if err != nil || !os.SameFile(current, t.info) {
		return fmt.Errorf("%w: path no longer names the target", ErrEditTarget)
	}
	return nil
}

// overwrite replaces the open file's contents with data. It writes first and
// truncates after, so a failed write never leaves a shorter file than a
// successful one would.
// Every failure wraps ErrSaveIncomplete: the file may already hold some of
// data.
func (t *editTarget) overwrite(data []byte) error {
	if _, err := t.file.WriteAt(data, 0); err != nil {
		return fmt.Errorf("%w: write target: %w", ErrSaveIncomplete, err)
	}
	if err := t.file.Truncate(int64(len(data))); err != nil {
		return fmt.Errorf("%w: truncate target: %w", ErrSaveIncomplete, err)
	}
	if err := t.file.Sync(); err != nil {
		return fmt.Errorf("%w: sync target: %w", ErrSaveIncomplete, err)
	}
	return nil
}
