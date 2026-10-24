package engine

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// GitSyncBackend is optional so older host backends remain compatible.
type GitSyncBackend interface {
	GitSync(context.Context, string, files.GitSyncRequest) (files.GitSyncResult, error)
}

func (e *Engine) GitSync(ctx context.Context, dir string, req files.GitSyncRequest) (files.GitSyncResult, error) {
	if !e.svc.knownDirectory(dir) {
		return files.GitSyncResult{Op: req.Op}, fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitSync(ctx, dir, req)
}
