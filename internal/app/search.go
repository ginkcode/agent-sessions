package app

import (
	"github.com/ginkcode/agent-sessions/internal/index"
)

// Search runs a full-text query over the private FTS index.
func (a *App) Search(query string, filter index.SearchFilter) ([]index.SearchHit, error) {
	return a.activeBackend().Search(a.appCtx(), query, filter)
}

// IndexProgress reports the background FTS indexing queue status.
func (a *App) IndexProgress() (index.FTSProgress, error) {
	return a.activeBackend().IndexProgress(a.appCtx())
}
