package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func (a *App) gitWriteRoute(host string) (engine.GitWriteBackend, error) {
	r := a.route()
	if r.host != host {
		return nil, ErrSaveHostChanged
	}
	if offline, ok := r.backend.(offlineBackend); ok {
		return nil, offline.err()
	}
	b, ok := r.backend.(engine.GitWriteBackend)
	if !ok {
		return nil, engine.ErrUnsupportedAction
	}
	return b, nil
}
func (a *App) GitStage(dir string, paths []string, host string) error {
	b, err := a.gitWriteRoute(host)
	if err != nil {
		return err
	}
	return b.GitStage(a.appCtx(), dir, paths)
}
func (a *App) GitUnstage(dir string, paths []string, host string) error {
	b, err := a.gitWriteRoute(host)
	if err != nil {
		return err
	}
	return b.GitUnstage(a.appCtx(), dir, paths)
}
func (a *App) GitDiscard(dir string, req files.GitDiscardRequest, host string) error {
	b, err := a.gitWriteRoute(host)
	if err != nil {
		return err
	}
	return b.GitDiscard(a.appCtx(), dir, req)
}
func (a *App) GitCommit(dir string, req files.GitCommitRequest, host string) error {
	b, err := a.gitWriteRoute(host)
	if err != nil {
		return err
	}
	return b.GitCommit(a.appCtx(), dir, req)
}
