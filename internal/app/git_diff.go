package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func (a *App) ReadGitDiff(dir, path string, basis files.GitDiffBasis) (files.GitDiffContent, error) {
	r := a.route()
	if offline, ok := r.backend.(offlineBackend); ok {
		return files.GitDiffContent{}, offline.err()
	}
	b, ok := r.backend.(engine.GitDiffBackend)
	if !ok {
		return files.GitDiffContent{}, engine.ErrUnsupportedAction
	}
	return b.ReadGitDiff(a.appCtx(), dir, path, basis)
}
