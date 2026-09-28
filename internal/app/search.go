package app

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/index"
)

// Search runs a full-text query over the private FTS index.
func (a *App) Search(query string, filter index.SearchFilter) ([]index.SearchHit, error) {
	if a.svc == nil {
		return nil, nil
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	db := a.searchDB()
	if db == nil {
		// Without a cache DB there is nothing indexed yet.
		return []index.SearchHit{}, nil
	}
	return db.Search(ctx, query, filter)
}

// IndexProgress reports the background FTS indexing queue status.
func (a *App) IndexProgress() (index.FTSProgress, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	db := a.searchDB()
	if db == nil {
		return index.FTSProgress{}, nil
	}
	return db.Progress(ctx)
}

func (a *App) searchDB() *index.DB {
	if a.refresher == nil {
		return nil
	}
	return a.refresher.DB()
}
