package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

// GitStatus reports changes on the selected host, never silently falling back locally.
func (a *App) GitStatus(dir string) (files.RepoStatus, error) {
	backend := a.activeBackend()
	if offline, ok := backend.(offlineBackend); ok {
		return files.RepoStatus{}, offline.err()
	}
	git, ok := backend.(engine.GitChangesBackend)
	if !ok {
		return files.RepoStatus{}, engine.ErrUnsupportedAction
	}
	return git.GitStatus(a.appCtx(), dir)
}
