// Package index manages the private SQLite database caching session metadata,
// scanner state, and FTS indexes.
package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// OnChangedFunc is called when a provider scan commits new, updated, or removed sessions.
type OnChangedFunc func(agent model.AgentID, changed []model.SessionRef, removed []model.SessionRef)

// RefresherOption configures a Refresher instance.
type RefresherOption func(*Refresher)

// WithWorkers configures worker concurrency for Bootstrap scans.
func WithWorkers(n int) RefresherOption {
	return func(r *Refresher) {
		if n > 0 {
			r.workers = n
		}
	}
}

// WithOnChanged sets the initial change notification callback.
func WithOnChanged(fn OnChangedFunc) RefresherOption {
	return func(r *Refresher) {
		r.onChanged = fn
	}
}

// Refresher coordinates incremental session scanning, private cache commits,
// and in-memory catalog synchronization across providers.
type Refresher struct {
	db        *DB
	catalog   *scan.Catalog
	providers provider.Set
	workers   int

	onChangedMu sync.RWMutex
	onChanged   OnChangedFunc

	generation atomic.Uint64

	locksMu sync.Mutex
	locks   map[model.AgentID]*sync.Mutex

	stateMu sync.RWMutex
	diag    map[model.AgentID]provider.Diagnostics
	errs    map[model.AgentID]error
	states  map[model.AgentID]provider.ScanState
}

// NewRefresher creates a Refresher with the given cache DB, in-memory catalog,
// and provider set.
func NewRefresher(db *DB, catalog *scan.Catalog, providers provider.Set, opts ...RefresherOption) *Refresher {
	r := &Refresher{
		db:        db,
		catalog:   catalog,
		providers: providers,
		workers:   3,
		locks:     make(map[model.AgentID]*sync.Mutex),
		diag:      make(map[model.AgentID]provider.Diagnostics),
		errs:      make(map[model.AgentID]error),
		states:    make(map[model.AgentID]provider.ScanState),
	}
	r.generation.Store(1)

	// Pre-populate persisted states if DB is available. A corrupt state is
	// recorded as the provider's error so it surfaces in diagnostics; the
	// next Refresh hard-fails until ClearCache resets it.
	if db != nil {
		for _, p := range providers {
			st, err := db.LoadState(context.Background(), p.ID())
			if err != nil {
				r.errs[p.ID()] = fmt.Errorf("load state: %w", err)
				continue
			}
			r.states[p.ID()] = st
		}
	}

	for _, opt := range opts {
		opt(r)
	}
	return r
}

// SetOnChanged sets or replaces the change notification callback.
func (r *Refresher) SetOnChanged(fn OnChangedFunc) {
	r.onChangedMu.Lock()
	defer r.onChangedMu.Unlock()
	r.onChanged = fn
}

// Generation returns the current refresher generation counter.
func (r *Refresher) Generation() uint64 {
	return r.generation.Load()
}

// BumpGeneration increments the generation counter, causing any in-flight
// asynchronous scan to be discarded before commit.
func (r *Refresher) BumpGeneration() uint64 {
	return r.generation.Add(1)
}

// DB returns the underlying cache database instance.
func (r *Refresher) DB() *DB {
	return r.db
}

// Catalog returns the underlying in-memory session catalog.
func (r *Refresher) Catalog() *scan.Catalog {
	return r.catalog
}

// Refresh performs an incremental scan for a single provider.
// One Refresh invocation per provider at a time; scan.Provider remains M0.
// On success, changed and removed session refs are returned.
func (r *Refresher) Refresh(ctx context.Context, p provider.Provider) ([]model.SessionRef, []model.SessionRef, error) {
	if p == nil {
		return nil, nil, errors.New("index: provider is nil")
	}

	// 1. Enforce single-flight per provider.
	pMu := r.getProviderLock(p.ID())
	pMu.Lock()
	defer pMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	// 2. Capture generation for staleness check after scan.
	gen := r.Generation()

	// 3. Load persisted provider_state and pass unmodified to p.Scan(ctx, prev).
	var prev provider.ScanState
	if r.db != nil {
		var err error
		prev, err = r.db.LoadState(ctx, p.ID())
		if err != nil {
			r.recordError(p.ID(), fmt.Errorf("load state: %w", err))
			return nil, nil, fmt.Errorf("index: load state for %s: %w", p.ID(), err)
		}
	} else {
		r.stateMu.RLock()
		prev = r.states[p.ID()]
		r.stateMu.RUnlock()
		if prev.Sources == nil {
			prev.Sources = make(map[string]provider.SourceState)
		}
	}

	// 4. Run provider Scan.
	result, err := p.Scan(ctx, prev)
	if err != nil {
		r.recordError(p.ID(), fmt.Errorf("scan: %w", err))
		return nil, nil, fmt.Errorf("index: scan %s: %w", p.ID(), err)
	}
	r.recordDiag(p.ID(), result.Diag)

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	// 5. Stale generation guard (e.g. ClearCache called during scan).
	if r.Generation() != gen {
		return nil, nil, nil
	}

	// Ensure agent is set on refs.
	for i := range result.Changed {
		if result.Changed[i].Ref.Agent == "" {
			result.Changed[i].Ref.Agent = p.ID()
		}
	}
	for i := range result.Removed {
		if result.Removed[i].Agent == "" {
			result.Removed[i].Agent = p.ID()
		}
	}

	hasChanges := len(result.Changed) > 0 || len(result.Removed) > 0
	stateAdvanced := hasStateChanged(prev, result.State)

	// If no changes and state has not advanced, nothing to commit or notify.
	if !hasChanges && !stateAdvanced {
		r.clearError(p.ID())
		return nil, nil, nil
	}

	// 6. DB.CommitScan first; then Catalog.Apply(result); then emit change event.
	// The per-provider single-flight lock held since step 1 also excludes a
	// concurrent ClearCache: ClearCache acquires every provider lock before
	// bumping the generation and rebuilding, so this commit cannot interleave
	// with a rebuild (the generation checks below are therefore stable).
	if r.db != nil {
		if err := r.db.CommitScan(ctx, p.ID(), result); err != nil {
			r.recordError(p.ID(), fmt.Errorf("commit scan: %w", err))
			return nil, nil, fmt.Errorf("index: commit scan for %s: %w", p.ID(), err)
		}
	}

	// Recheck generation after commit.
	if r.Generation() != gen {
		return nil, nil, nil
	}

	// Advance in-memory state and clear error.
	r.recordState(p.ID(), result.State)
	r.clearError(p.ID())

	// If no changes but scan state advanced, commit state without a UI event.
	if !hasChanges {
		return nil, nil, nil
	}

	// 7. Update in-memory Catalog and overlay live badges if available.
	if r.catalog != nil {
		if detector, ok := p.(provider.LiveDetector); ok && ctx.Err() == nil {
			live, liveErr := detector.Live(ctx)
			r.catalog.ApplyLive(p.ID(), result, live, liveErr, true)
			if liveErr != nil {
				r.recordError(p.ID(), fmt.Errorf("live: %w", liveErr))
			}
		} else {
			r.catalog.Apply(result)
		}
	}

	// 8. Build refs and notify listeners.
	changedRefs := make([]model.SessionRef, len(result.Changed))
	for i, m := range result.Changed {
		changedRefs[i] = m.Ref
	}
	removedRefs := make([]model.SessionRef, len(result.Removed))
	copy(removedRefs, result.Removed)

	r.onChangedMu.RLock()
	notify := r.onChanged
	r.onChangedMu.RUnlock()

	if notify != nil {
		notify(p.ID(), changedRefs, removedRefs)
	}

	return changedRefs, removedRefs, nil
}

// Bootstrap scans all detected providers concurrently and updates the cache.
// Startup calls index.Open and LoadCatalog first for instant first paint;
// Bootstrap runs asynchronously.
func (r *Refresher) Bootstrap(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var detected []provider.Provider
	for _, p := range r.providers {
		if err := ctx.Err(); err != nil {
			return err
		}
		d, err := p.Detect(ctx)
		if err != nil {
			r.recordError(p.ID(), fmt.Errorf("detect: %w", err))
		} else if d.Present {
			detected = append(detected, p)
		}
	}

	if len(detected) == 0 {
		return nil
	}

	workers := r.workers
	if workers <= 0 {
		workers = 3
	}
	workers = min(workers, len(detected))

	work := make(chan provider.Provider)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				if ctx.Err() != nil {
					return
				}
				_, _, _ = r.Refresh(ctx, p)
			}
		}()
	}

enqueue:
	for _, p := range detected {
		select {
		case <-ctx.Done():
			break enqueue
		case work <- p:
		}
	}
	close(work)

	wg.Wait()
	return ctx.Err()
}

// providerLocks snapshots every per-provider single-flight lock so ClearCache
// can exclude in-flight Refresh calls wholesale. Lock ordering is safe: a
// Refresh holds at most one lock at a time, so it can never wait on a lock
// another Refresh holds while ClearCache waits on it.
func (r *Refresher) providerLocks() []*sync.Mutex {
	r.locksMu.Lock()
	defer r.locksMu.Unlock()

	out := make([]*sync.Mutex, 0, len(r.locks))
	for _, mu := range r.locks {
		out = append(out, mu)
	}
	return out
}

// ClearCache bumps the generation, rebuilds the private SQLite cache DB,
// and only after a successful rebuild clears the in-memory catalog and
// states. It holds every provider's single-flight lock throughout so an
// in-flight Refresh cannot commit into the freshly rebuilt database.
func (r *Refresher) ClearCache(ctx context.Context) error {
	locks := r.providerLocks()
	for _, mu := range locks {
		mu.Lock()
	}
	defer func() {
		for _, mu := range locks {
			mu.Unlock()
		}
	}()

	r.BumpGeneration()

	if r.db != nil {
		if err := r.db.Rebuild(ctx); err != nil {
			return fmt.Errorf("index: rebuild db: %w", err)
		}
	}

	// Drop in-memory state only after a successful rebuild so a failed
	// rebuild leaves the previous catalog listings usable.
	if r.catalog != nil {
		r.catalog.Reset(nil)
	}

	r.stateMu.Lock()
	r.states = make(map[model.AgentID]provider.ScanState)
	r.diag = make(map[model.AgentID]provider.Diagnostics)
	r.errs = make(map[model.AgentID]error)
	r.stateMu.Unlock()
	return nil
}

// Report returns an aggregated snapshot of provider diagnostics, errors, and scan states.
func (r *Refresher) Report() scan.Report {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()

	rep := scan.Report{
		States: make(map[model.AgentID]provider.ScanState, len(r.states)),
		Diag:   make(map[model.AgentID]provider.Diagnostics, len(r.diag)),
		Errors: make(map[model.AgentID]error, len(r.errs)),
	}
	for k, v := range r.states {
		rep.States[k] = v
	}
	for k, v := range r.diag {
		rep.Diag[k] = v
	}
	for k, v := range r.errs {
		rep.Errors[k] = v
	}
	return rep
}

// Diagnostics returns aggregated scanner diagnostics across all providers.
func (r *Refresher) Diagnostics() (provider.Diagnostics, error) {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()

	var total provider.Diagnostics
	for _, d := range r.diag {
		total.Merge(d)
	}
	return total, nil
}

// DiagnosticsByProvider returns diagnostics keyed by provider ID.
func (r *Refresher) DiagnosticsByProvider() map[string]provider.Diagnostics {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()

	out := make(map[string]provider.Diagnostics, len(r.diag))
	for id, d := range r.diag {
		out[string(id)] = d
	}
	return out
}

// ScanErrors returns the last scan error string keyed by provider ID.
func (r *Refresher) ScanErrors() map[string]string {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()

	out := make(map[string]string, len(r.errs))
	for id, err := range r.errs {
		out[string(id)] = err.Error()
	}
	return out
}

// States returns a copy of the current in-memory provider scan states.
func (r *Refresher) States() map[model.AgentID]provider.ScanState {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()

	out := make(map[model.AgentID]provider.ScanState, len(r.states))
	for id, st := range r.states {
		out[id] = st
	}
	return out
}

// Close closes the underlying cache database if present.
func (r *Refresher) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

func (r *Refresher) getProviderLock(agent model.AgentID) *sync.Mutex {
	r.locksMu.Lock()
	defer r.locksMu.Unlock()
	mu, ok := r.locks[agent]
	if !ok {
		mu = &sync.Mutex{}
		r.locks[agent] = mu
	}
	return mu
}

func (r *Refresher) recordError(agent model.AgentID, err error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.errs[agent] = err
}

func (r *Refresher) clearError(agent model.AgentID) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	delete(r.errs, agent)
}

func (r *Refresher) recordDiag(agent model.AgentID, diag provider.Diagnostics) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.diag[agent] = diag
}

func (r *Refresher) recordState(agent model.AgentID, st provider.ScanState) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.states[agent] = st
}

func hasStateChanged(old, next provider.ScanState) bool {
	if old.Cursor != next.Cursor {
		return true
	}
	if len(old.Sources) != len(next.Sources) {
		return true
	}
	for path, nextSrc := range next.Sources {
		oldSrc, ok := old.Sources[path]
		if !ok {
			return true
		}
		if oldSrc.Size != nextSrc.Size ||
			oldSrc.ModTimeNs != nextSrc.ModTimeNs ||
			oldSrc.Offset != nextSrc.Offset ||
			!bytes.Equal(oldSrc.Checkpoint, nextSrc.Checkpoint) {
			return true
		}
	}
	return false
}
