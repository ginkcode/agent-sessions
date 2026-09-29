//go:build darwin

package manage

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
)

// FinderTrash implements Trash by asking Finder to move one item to Trash.
// Finder preserves the item's put-back location and handles non-startup volumes.
type FinderTrash struct {
	Exec func(ctx context.Context, argv []string) error
}

// Name identifies the transport in diagnostics.
func (FinderTrash) Name() string { return "finder" }

// Trash moves path to the macOS Trash. The path is passed as a separate
// osascript argument and bound by reference, so it cannot change the script.
func (f FinderTrash) Trash(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.New("trash path is invalid")
	}
	script := `on run argv
set targetPath to item 1 of argv
tell application "Finder" to delete (POSIX file targetPath as alias)
end run`
	if err := f.Exec(context.Background(), []string{"osascript", "-e", script, abs}); err != nil {
		return errors.New("trash operation failed")
	}
	return nil
}

// NewFinderTrash verifies that the macOS scripting runtime is available.
func NewFinderTrash() (FinderTrash, error) {
	if _, err := exec.LookPath("osascript"); err != nil {
		return FinderTrash{}, errors.New("manage: osascript not found")
	}
	return FinderTrash{Exec: runExec}, nil
}

// platformTrash returns the macOS Finder-backed Trash transport.
func platformTrash() (Trash, error) {
	return NewFinderTrash()
}
