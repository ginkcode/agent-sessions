package engine

import (
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
)

// purgeDelay batches the cache purge after a burst of deletions: a purge
// rewrites the whole full-text index.
var purgeDelay = 10 * time.Second

// schedulePurge removes deleted sessions' text from the cache files a
// little later: what the database file and its log still hold (see
// index.DB.Purge), and the copies in databases of other app versions (see
// index.DropCopies). Only the cache writer does either.
func (e *Engine) schedulePurge() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.runCtx == nil || e.purgePending {
		return
	}
	e.purgePending = true
	ctx, delay := e.runCtx, purgeDelay
	e.bg.Go(func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		e.mu.Lock()
		e.purgePending = false
		refresher := e.refresher
		writer := e.lock != nil
		e.mu.Unlock()
		if refresher == nil || !writer {
			return
		}
		e.dbUse.RLock()
		_ = refresher.Purge(ctx)
		e.dbUse.RUnlock()
		index.DropCopies(e.cacheDir(), e.indexKey())
	})
}
