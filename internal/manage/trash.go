// Package manage — trash transport definitions.
package manage

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Action identifies a destructive transport.
type Action string

const (
	// ActionTrash moves files to the system Trash; recoverable.
	ActionTrash Action = "trash"
	// ActionDelete permanently deletes; provider-specific.
	ActionDelete Action = "delete"
)

// ErrUnsupportedAction is returned when an agent has no delete transport.
var ErrUnsupportedAction = errors.New("unsupported action for this agent")

// ErrDisabled is returned when the [manage] enabled setting is off.
var ErrDisabled = errors.New("destructive actions are disabled; enable them in settings")

// ErrLive is returned when a target session (or one of its children) is
// currently live and must not be deleted.
var ErrLive = errors.New("session is live")

// ErrPreviewStale is returned when the confirmation token does not match
// the current plan; the caller must re-preview.
var ErrPreviewStale = errors.New("preview is stale; retry the operation")

// Trash moves one path out of view, ideally to a recoverable location.
type Trash interface {
	Trash(path string) error
	Name() string
}

// GioTrash implements Trash with one `gio trash -- <path>` call per path.
// If gio is unavailable it fails; it never falls back to os.Remove.
type GioTrash struct {
	Exec func(ctx context.Context, argv []string) error
}

// Name identifies the transport in diagnostics.
func (GioTrash) Name() string { return "gio" }

// Trash moves path to the Trash.
func (g GioTrash) Trash(path string) error {
	if err := g.Exec(context.Background(), []string{"gio", "trash", "--", path}); err != nil {
		return fmt.Errorf("gio trash: %w", err)
	}
	return nil
}

// NewGioTrash builds a GioTrash around a real process executor and fails
// when gio is not installed, so callers can disable trashing up front.
func NewGioTrash() (GioTrash, error) {
	if _, err := exec.LookPath("gio"); err != nil {
		return GioTrash{}, errors.New("manage: gio not found")
	}
	return GioTrash{Exec: runExec}, nil
}

// runExec runs argv without a shell.
func runExec(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec
	var stderr []byte
	cmd.Stderr = &stderrWriter{buf: &stderr}
	if err := cmd.Run(); err != nil {
		_ = stderr // captured but not surfaced; gio errors can embed paths
		return errors.New("trash command failed")
	}
	return nil
}

type stderrWriter struct {
	buf *[]byte
}

func (w *stderrWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}
