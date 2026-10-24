package app

import (
	"errors"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

// GitSync fetches, fast-forward pulls or pushes on the host the caller saw.
// syncID names the run for CancelGitSync; a new run under the same ID cancels
// the old one. Failures are "<code>: <message>" (files.GitSyncError).
func (a *App) GitSync(dir string, req files.GitSyncRequest, syncID, host string) (files.GitSyncResult, error) {
	r := a.route()
	if r.host != host {
		return files.GitSyncResult{Op: req.Op}, ErrSaveHostChanged
	}
	if offline, ok := r.backend.(offlineBackend); ok {
		return files.GitSyncResult{Op: req.Op}, offline.err()
	}
	b, ok := r.backend.(engine.GitSyncBackend)
	if !ok {
		return files.GitSyncResult{Op: req.Op}, engine.ErrUnsupportedAction
	}
	ctx, done := a.gitSyncs.start(a.appCtx(), syncID)
	defer done()
	out, err := b.GitSync(ctx, dir, req)
	var syncErr *files.GitSyncError
	// A remote client returns ctx.Err() itself once it sends the cancel.
	if err != nil && ctx.Err() != nil && !errors.As(err, &syncErr) {
		err = files.NewGitSyncError(files.GitSyncCanceled, fmt.Sprintf("git %s was canceled.", req.Op))
	}
	return out, err
}

// CancelGitSync stops the run started under syncID, killing its Git process
// group. It is a no-op once that run has finished.
func (a *App) CancelGitSync(syncID string) {
	a.gitSyncs.cancel(syncID)
}
