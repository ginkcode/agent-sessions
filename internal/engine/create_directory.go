package engine

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// CreateDirectory creates a directory in a catalog-authorized project.
func (e *Engine) CreateDirectory(ctx context.Context, dir, path, name string) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.CreateDirectory(ctx, e.editPolicy(), dir, path, name)
}

// CreateFile creates an empty file in a catalog-authorized project.
func (e *Engine) CreateFile(ctx context.Context, dir, path, name string) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.CreateFile(ctx, e.editPolicy(), dir, path, name)
}
