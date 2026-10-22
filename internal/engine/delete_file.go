package engine

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// DeleteFile permanently removes a catalog-authorized file or directory.
func (e *Engine) DeleteFile(ctx context.Context, dir, path string) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.Delete(ctx, e.editPolicy(), dir, path)
}
