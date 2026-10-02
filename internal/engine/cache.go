package engine

import (
	"context"
	"errors"
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// Several engines can share one cache dir: desktop apps and remote servers,
// possibly of different versions. Engines of one index schema that scan the
// same provider roots share an index (see indexKey); of those, the engine
// holding its lock is the writer: it commits scans and builds the FTS index. The others
// are readers: they seed their catalog from the writer's database, scan and
// watch in memory, search the writer's FTS index, and take over as writer
// when the old one exits.

// defaultPromoteEvery is how often a reader retries the writer lock.
const defaultPromoteEvery = 5 * time.Second

// indexKey names this engine's index by the provider roots it scans, so an
// engine never reads or overwrites another set of roots' sessions.
func (e *Engine) indexKey() string {
	return index.RootsKey(e.roots.Claude, e.roots.Codex, e.roots.OpenCodeData)
}

// startCache starts the engine as writer or reader. It returns false when
// the cache cannot be used at all, so Start falls back to an in-memory scan.
// Called with e.mu held.
func (e *Engine) startCache(ctx context.Context) bool {
	dir, key := e.cacheDir(), e.indexKey()
	lock, err := index.TryLock(dir, key)
	switch {
	case err == nil:
		return e.startWriter(ctx, dir, key, lock)
	case errors.Is(err, index.ErrLocked):
		e.startReader(ctx, dir, key)
		return true
	default:
		return false
	}
}

func (e *Engine) startWriter(ctx context.Context, dir, key string, lock *index.Lock) bool {
	db, metas, err := openWriter(ctx, dir, key)
	if err != nil {
		_ = lock.Unlock()
		return false
	}
	e.lock = lock
	if e.svc.Catalog() != nil {
		e.svc.Catalog().Reset(metas)
	}
	e.events.NotifyFullRefresh()

	e.refresher = index.NewRefresher(db, e.svc.Catalog(), e.svc.Providers(), index.WithOnChanged(e.onChanged))
	e.indexer = index.StartIndexer(ctx, db, e.svc.Providers(), e.emitProgress)
	e.closeWatch = e.startWatcher(ctx)
	e.bootstrap(ctx, e.refresher)
	e.bg.Go(func() { index.Cleanup(dir, key, index.UnusedFor) })
	return true
}

// openWriter opens the database as its writer and loads the catalog. A
// catalog that cannot be read is rebuilt from scratch rather than kept, so
// a bad row cannot stop every engine from becoming the writer.
func openWriter(ctx context.Context, dir, key string) (*index.DB, []model.SessionMeta, error) {
	db, err := index.Open(ctx, dir, key)
	if err != nil {
		return nil, nil, err
	}
	metas, err := db.LoadCatalog(ctx)
	if err == nil {
		return db, metas, nil
	}
	if err := db.Rebuild(ctx); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return db, nil, nil
}

// startReader seeds the catalog and scan states from the writer's database
// in one read, so the first scans only pick up what changed since. Without
// a readable database yet it starts empty and scans everything.
func (e *Engine) startReader(ctx context.Context, dir, key string) {
	var metas []model.SessionMeta
	var opts []index.RefresherOption
	if db, err := index.OpenReadOnly(ctx, dir, key); err == nil {
		m, states, err := db.LoadSnapshot(ctx)
		if err == nil {
			metas = m
			opts = append(opts, index.WithStates(states))
		}
		e.readDB = db
	}
	if e.svc.Catalog() != nil {
		e.svc.Catalog().Reset(metas)
	}
	e.events.NotifyFullRefresh()

	opts = append(opts, index.WithOnChanged(e.onChanged))
	e.refresher = index.NewRefresher(nil, e.svc.Catalog(), e.svc.Providers(), opts...)
	e.closeWatch = e.startWatcher(ctx)
	e.bootstrap(ctx, e.refresher)
	e.bg.Go(func() { e.promoteLoop(ctx, dir, key) })
}

// promoteLoop retries the writer lock until it gets it. Between tries it
// keeps the read-only handle current and forwards the writer's indexing
// progress, which this process cannot observe otherwise.
func (e *Engine) promoteLoop(ctx context.Context, dir, key string) {
	t := time.NewTicker(e.promoteEvery)
	defer t.Stop()
	var last index.FTSProgress
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if lock, err := index.TryLock(dir, key); err == nil {
			if e.promote(ctx, dir, key, lock) {
				return
			}
			continue
		}
		db := e.currentReadDB(ctx, dir, key)
		if db == nil {
			continue
		}
		if p, err := db.Progress(ctx); err == nil && p != last {
			last = p
			e.emitProgress(p)
		}
	}
}

// currentReadDB returns the reader's handle, opening it if the writer has
// created the database since, or reopening it if the writer replaced it.
func (e *Engine) currentReadDB(ctx context.Context, dir, key string) *index.DB {
	e.mu.Lock()
	db := e.readDB
	e.mu.Unlock()
	if db != nil && !db.Replaced() {
		return db
	}
	fresh, err := index.OpenReadOnly(ctx, dir, key)
	if err != nil {
		return db
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		_ = fresh.Close()
		return nil
	}
	e.readDB = fresh
	e.mu.Unlock()
	e.closeDB(db)
	return fresh
}

// closeDB closes a database once no Search or IndexProgress is using it.
// Call it without e.mu held.
func (e *Engine) closeDB(db *index.DB) {
	if db == nil {
		return
	}
	e.dbUse.Lock()
	defer e.dbUse.Unlock()
	_ = db.Close()
}

// promote makes this reader the writer. It reports whether the promoter is
// done: false means opening the database failed, the lock was released and
// the reader carries on.
func (e *Engine) promote(ctx context.Context, dir, key string, lock *index.Lock) bool {
	db, metas, err := openWriter(ctx, dir, key)
	if err != nil {
		_ = lock.Unlock()
		return ctx.Err() != nil
	}

	catalog := e.svc.Catalog()
	refresher := index.NewRefresher(db, catalog, e.svc.Providers(), index.WithOnChanged(e.onChanged))
	indexer := index.StartIndexer(ctx, db, e.svc.Providers(), e.emitProgress)

	// Swapped in before the old refresher retires, so no watcher event or
	// Scan in between reaches a refresher that ignores it.
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		indexer.Close()
		_ = db.Close()
		_ = lock.Unlock()
		return true
	}
	old := e.refresher
	readDB := e.readDB
	e.refresher = refresher
	e.indexer = indexer
	e.lock = lock
	e.readDB = nil
	events := e.events
	e.mu.Unlock()
	if old != nil {
		old.Retire()
	}

	// The catalog must cover the database's scan states: a session the old
	// writer committed that this reader had not seen yet would never be
	// reported as changed again. What this reader scanned stays, and so do
	// its live badges; the bootstrap below commits it and drops whatever
	// the old writer still had but is gone.
	if catalog != nil {
		catalog.Reset(mergeCatalog(metas, catalog.All()))
	}
	e.closeDB(readDB)
	e.svc.ClearTranscripts()
	if events != nil {
		events.NotifyFullRefresh()
	}
	e.bootstrap(ctx, refresher)
	e.bg.Go(func() { index.Cleanup(dir, key, index.UnusedFor) })
	return true
}

func (e *Engine) bootstrap(ctx context.Context, r *index.Refresher) {
	e.bg.Go(func() {
		_ = r.Bootstrap(ctx)
		e.svc.ApplyReport(r.Report())
	})
}

func (e *Engine) onChanged(_ model.AgentID, changed, removed []model.SessionRef) {
	e.mu.Lock()
	indexer := e.indexer
	events := e.events
	e.mu.Unlock()
	if e.svc != nil {
		e.svc.EvictTranscripts(changed, removed)
	}
	if indexer != nil {
		indexer.Notify()
	}
	if events != nil {
		events.NoteChanged(changed, removed)
	}
}

func (e *Engine) emitProgress(p index.FTSProgress) {
	e.mu.Lock()
	emitter := e.emitter
	e.mu.Unlock()
	if emitter != nil {
		emitter.Emit("index:progress", p)
	}
}

// searchDB is the writer's database, or a reader's read-only handle on it;
// reader is true for the latter. Hold e.dbUse shared while using it.
func (e *Engine) searchDB() (db *index.DB, reader bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.refresher != nil && e.refresher.DB() != nil {
		return e.refresher.DB(), false
	}
	return e.readDB, e.readDB != nil
}

// mergeCatalog is the database's sessions updated with this engine's own:
// where both have a session, the engine's copy is the newer scan.
func mergeCatalog(db, own []model.SessionMeta) []model.SessionMeta {
	byKey := make(map[string]int, len(db))
	out := make([]model.SessionMeta, 0, len(db)+len(own))
	for _, m := range db {
		byKey[m.Ref.Key()] = len(out)
		out = append(out, m)
	}
	for _, m := range own {
		if i, ok := byKey[m.Ref.Key()]; ok {
			out[i] = m
			continue
		}
		out = append(out, m)
	}
	return out
}
