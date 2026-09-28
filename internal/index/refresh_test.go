package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func newTestRefresher(t *testing.T, provs ...provider.Provider) (*Refresher, *DB) {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRefresher(db, scan.NewCatalog(), provider.Set(provs)), db
}

func refresherMeta(agent model.AgentID, id string, updated time.Time) model.SessionMeta {
	return model.SessionMeta{
		Ref:       model.SessionRef{Agent: agent, ID: id},
		Title:     "session " + id,
		UpdatedAt: updated,
	}
}

// TestRefreshCommitOrderAndRefs verifies commit order, ref normalization, and callbacks.
func TestRefreshCommitOrderAndRefs(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "s1", time.Now().UTC())}
	p.StateOverride = &provider.ScanState{
		Sources: map[string]provider.SourceState{
			"/tmp/a.jsonl": {Size: 10, ModTimeNs: 1, Offset: 10},
		},
	}

	r, db := newTestRefresher(t, p)

	var mu sync.Mutex
	var notified bool
	var notifiedChanged, notifiedRemoved []model.SessionRef
	r.SetOnChanged(func(agent model.AgentID, changed, removed []model.SessionRef) {
		mu.Lock()
		defer mu.Unlock()
		notified = true
		notifiedChanged = changed
		notifiedRemoved = removed
	})

	changed, removed, err := r.Refresh(context.Background(), p)
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if len(changed) != 1 || changed[0].Key() != "claude-code:s1" {
		t.Errorf("unexpected changed refs: %+v", changed)
	}
	if len(removed) != 0 {
		t.Errorf("expected no removed refs, got %+v", removed)
	}

	mu.Lock()
	wasNotified := notified
	mu.Unlock()
	if !wasNotified {
		t.Error("expected OnChanged to fire after commit")
	}

	// Verify the DB now has the session.
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 1 || metas[0].Ref.ID != "s1" {
		t.Errorf("expected one cached session s1, got %+v", metas)
	}

	// Verify the in-memory catalog now has the session.
	if m, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); !ok || m.Title != "session s1" {
		t.Errorf("expected catalog to contain s1, got %+v", m)
	}

	mu.Lock()
	ch, rm := notifiedChanged, notifiedRemoved
	mu.Unlock()
	if len(ch) != 1 || ch[0].Key() != "claude-code:s1" {
		t.Errorf("expected notified changed ref claude-code:s1, got %+v", ch)
	}
	if len(rm) != 0 {
		t.Errorf("expected no notified removed refs, got %+v", rm)
	}
}

// TestRefreshUnchangedScan verifies a no-change scan emits no callback.
func TestRefreshUnchangedScan(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, _ := newTestRefresher(t, p)

	notified := false
	r.SetOnChanged(func(model.AgentID, []model.SessionRef, []model.SessionRef) {
		notified = true
	})

	if _, _, err := r.Refresh(context.Background(), p); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if notified {
		t.Error("expected no change callback on unchanged scan")
	}
}

// TestRefreshStateAdvancedOnlyCommit verifies that when the state advances without
// session changes, the state is committed but no change event fires.
func TestRefreshStateAdvancedOnlyCommit(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.StateOverride = &provider.ScanState{
		Sources: map[string]provider.SourceState{
			"/tmp/only.jsonl": {Size: 123, ModTimeNs: 456, Offset: 123},
		},
	}

	r, db := newTestRefresher(t, p)

	notified := false
	r.SetOnChanged(func(model.AgentID, []model.SessionRef, []model.SessionRef) {
		notified = true
	})

	if _, _, err := r.Refresh(context.Background(), p); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if notified {
		t.Error("expected no change callback for state-only advance")
	}

	st, err := db.LoadState(context.Background(), model.AgentClaude)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	src, ok := st.Sources["/tmp/only.jsonl"]
	if !ok || src.Size != 123 {
		t.Errorf("expected persisted source state, got %+v", st)
	}
}

// TestRefreshPrevStatePassed verifies the persisted state flows into Scan unchanged.
func TestRefreshPrevStatePassed(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}

	r, db := newTestRefresher(t, p)

	// Pre-seed a state.
	prev := provider.ScanState{
		Sources: map[string]provider.SourceState{
			"/tmp/preseed.jsonl": {Size: 42, ModTimeNs: 99, Offset: 42},
		},
		Cursor: "seed-cursor",
	}
	if err := db.CommitScan(context.Background(), model.AgentClaude, provider.ScanResult{State: prev}); err != nil {
		t.Fatalf("seed CommitScan failed: %v", err)
	}

	if _, _, err := r.Refresh(context.Background(), p); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}

	got := p.LastPrev
	if got.Cursor != "seed-cursor" {
		t.Errorf("expected Cursor seed-cursor, got %q", got.Cursor)
	}
	if src, ok := got.Sources["/tmp/preseed.jsonl"]; !ok || src.Size != 42 {
		t.Errorf("expected preseeded source, got %+v", got)
	}
}

// TestRefreshSingleFlight ensures two concurrent Refresh calls for the same
// provider serialize rather than race.
func TestRefreshSingleFlight(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "s1", time.Now().UTC())}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, _ := newTestRefresher(t, p)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := r.Refresh(context.Background(), p); err != nil {
				t.Errorf("Refresh failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestRefreshGenerationStale ensures a bump between scan and commit discards the result.
func TestRefreshGenerationStale(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "stale", time.Now().UTC())}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, db := newTestRefresher(t, p)

	notified := false
	r.SetOnChanged(func(model.AgentID, []model.SessionRef, []model.SessionRef) {
		notified = true
	})

	p.ScanDelay = 50 * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := r.Refresh(context.Background(), p); err != nil {
			t.Errorf("Refresh failed: %v", err)
		}
	}()

	// Wait for Scan to enter its delay, then invalidate.
	time.Sleep(10 * time.Millisecond)
	r.BumpGeneration()
	<-done

	if notified {
		t.Error("stale scan must not notify OnChanged")
	}
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("stale scan must not commit sessions, got %+v", metas)
	}
}

// TestBootstrapScansDetectedProviders verifies that Bootstrap scans each
// detected provider exactly once and populates both the DB and the catalog.
func TestBootstrapScansDetectedProviders(t *testing.T) {
	a := providertest.NewFake(model.AgentClaude, "Claude")
	a.DetectionData = provider.Detection{Present: true}
	a.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "a1", time.Now().UTC())}
	a.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	b := providertest.NewFake(model.AgentCodex, "Codex")
	b.DetectionData = provider.Detection{Present: true}
	b.Sessions = []model.SessionMeta{refresherMeta(model.AgentCodex, "b1", time.Now().UTC())}
	b.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	absent := providertest.NewFake(model.AgentOpenCode, "OpenCode")
	absent.DetectionData = provider.Detection{Present: false}

	r, db := newTestRefresher(t, a, b, absent)
	if err := r.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	if a.ScanCalled != 1 || b.ScanCalled != 1 {
		t.Errorf("detected providers must scan exactly once; a=%d b=%d", a.ScanCalled, b.ScanCalled)
	}
	if absent.ScanCalled != 0 {
		t.Errorf("absent provider must not scan; scanned %d times", absent.ScanCalled)
	}

	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 sessions, got %+v", metas)
	}
	if m, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "a1"}); !ok || m.Ref.ID != "a1" {
		t.Errorf("expected catalog entry a1")
	}
}

// TestBootstrapDetectErrorIsolated ensures a failing detect does not block others.
func TestBootstrapDetectErrorIsolated(t *testing.T) {
	bad := providertest.NewFake(model.AgentClaude, "Claude")
	bad.DetectErr = errors.New("no store")

	good := providertest.NewFake(model.AgentCodex, "Codex")
	good.DetectionData = provider.Detection{Present: true}
	good.Sessions = []model.SessionMeta{refresherMeta(model.AgentCodex, "g1", time.Now().UTC())}
	good.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, db := newTestRefresher(t, bad, good)
	if err := r.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	errs := r.ScanErrors()
	if errs["claude-code"] == "" {
		t.Error("expected detect error recorded for claude-code")
	}
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 1 || metas[0].Ref.Agent != model.AgentCodex {
		t.Errorf("expected codex session only, got %+v", metas)
	}
}

// TestBootstrapScanErrorIsolated ensures a failing scan does not block others.
func TestBootstrapScanErrorIsolated(t *testing.T) {
	bad := providertest.NewFake(model.AgentClaude, "Claude")
	bad.DetectionData = provider.Detection{Present: true}
	bad.ScanErr = errors.New("disk unavailable")

	good := providertest.NewFake(model.AgentCodex, "Codex")
	good.DetectionData = provider.Detection{Present: true}
	good.Sessions = []model.SessionMeta{refresherMeta(model.AgentCodex, "g1", time.Now().UTC())}
	good.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, db := newTestRefresher(t, bad, good)
	if err := r.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}

	errs := r.ScanErrors()
	if errs["claude-code"] == "" {
		t.Error("expected scan error recorded for claude-code")
	}
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 1 || metas[0].Ref.Agent != model.AgentCodex {
		t.Errorf("expected codex session only, got %+v", metas)
	}
}

// TestRefreshRemovedRefs verifies removals propagate to the cache, the catalog,
// and the change callback.
func TestRefreshRemovedRefs(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "s1", time.Now().UTC())}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, db := newTestRefresher(t, p)
	if _, _, err := r.Refresh(context.Background(), p); err != nil {
		t.Fatalf("initial Refresh failed: %v", err)
	}

	// Now remove s1.
	p.Sessions = nil
	p.RemovedRefs = []model.SessionRef{{Agent: model.AgentClaude, ID: "s1"}}

	var mu sync.Mutex
	removedNotified := false
	r.SetOnChanged(func(_ model.AgentID, _ []model.SessionRef, removed []model.SessionRef) {
		mu.Lock()
		defer mu.Unlock()
		if len(removed) == 1 && removed[0].Key() == "claude-code:s1" {
			removedNotified = true
		}
	})

	changed, removed, err := r.Refresh(context.Background(), p)
	if err != nil {
		t.Fatalf("second Refresh failed: %v", err)
	}
	if len(changed) != 0 || len(removed) != 1 {
		t.Errorf("expected only removal, got changed=%+v removed=%+v", changed, removed)
	}

	mu.Lock()
	ok := removedNotified
	mu.Unlock()
	if !ok {
		t.Error("expected removal to notify OnChanged")
	}

	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("expected empty catalog after removal, got %+v", metas)
	}
	if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); ok {
		t.Error("expected s1 gone from in-memory catalog")
	}
}

// TestForgetDeletesRows verifies Forget removes sessions from the cache DB
// and the in-memory catalog while leaving unrelated sessions intact, that
// removal is idempotent for unknown refs, and that an in-flight Refresh for
// the same provider cannot commit a forgotten session back afterwards.
func TestForgetDeletesRows(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{
		refresherMeta(model.AgentClaude, "s1", time.Now().UTC()),
		refresherMeta(model.AgentClaude, "s2", time.Now().UTC().Add(time.Minute)),
	}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, db := newTestRefresher(t, p)
	ctx := context.Background()

	if _, _, err := r.Refresh(ctx, p); err != nil {
		t.Fatalf("initial Refresh failed: %v", err)
	}
	if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); !ok {
		t.Fatal("precondition: s1 missing from catalog")
	}

	// Forget one session and a never-seen one; both must be gone, s2 stays.
	forgetRefs := []model.SessionRef{
		{Agent: model.AgentClaude, ID: "s1"},
		{Agent: model.AgentClaude, ID: "unknown"},
	}
	if err := r.Forget(ctx, forgetRefs); err != nil {
		t.Fatalf("Forget failed: %v", err)
	}

	if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); ok {
		t.Error("expected s1 gone from in-memory catalog after Forget")
	}
	metas, err := db.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 1 || metas[0].Ref.ID != "s2" {
		t.Errorf("expected only s2 in cache DB, got %+v", metas)
	}
	var count int
	if err := db.SQLDB().QueryRowContext(ctx,
		"SELECT count(*) FROM sessions WHERE agent = ? AND id = ?", "claude-code", "s1").Scan(&count); err != nil {
		t.Fatalf("count s1 rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows for s1, got %d", count)
	}

	// Forget while a Refresh is in flight: the in-flight scan must not
	// commit the forgotten session back into the cache.
	p.ScanDelay = 50 * time.Millisecond
	p.ScanStarted = make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := r.Refresh(ctx, p); err != nil {
			t.Errorf("in-flight Refresh failed: %v", err)
		}
	}()

	select {
	case <-p.ScanStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("scan never entered")
	}

	if err := r.Forget(ctx, []model.SessionRef{{Agent: model.AgentClaude, ID: "s2"}}); err != nil {
		t.Fatalf("Forget during in-flight Refresh failed: %v", err)
	}
	<-done

	if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s2"}); ok {
		t.Error("in-flight Refresh resurrected forgotten session s2 in catalog")
	}
	metas, err = db.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog after in-flight Forget failed: %v", err)
	}
	for _, m := range metas {
		if m.Ref.ID == "s2" {
			t.Errorf("in-flight Refresh resurrected s2 in cache DB: %+v", metas)
			break
		}
	}
}

// TestClearCacheInvalidatesInFlight ensures ClearCache prevents a stale
// in-flight scan from committing afterwards.
func TestClearCacheInvalidatesInFlight(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "stale", time.Now().UTC())}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}
	p.ScanDelay = 50 * time.Millisecond
	p.ScanStarted = make(chan struct{}, 1)

	r, db := newTestRefresher(t, p)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := r.Refresh(context.Background(), p); err != nil {
			t.Errorf("Refresh failed: %v", err)
		}
	}()

	// Wait deterministically until Scan has been entered.
	select {
	case <-p.ScanStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("scan never entered")
	}
	if err := r.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache failed: %v", err)
	}
	<-done

	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("stale in-flight scan must not commit after ClearCache, got %+v", metas)
	}
}

// TestRefreshNoDBStillAppliesCatalog ensures Refresh works with a nil DB
// (catalog-only mode), useful for tests and headless runs.
func TestRefreshNoDBStillAppliesCatalog(t *testing.T) {
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	p.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "s1", time.Now().UTC())}
	p.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r := NewRefresher(nil, scan.NewCatalog(), provider.Set{p})
	changed, _, err := r.Refresh(context.Background(), p)
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if len(changed) != 1 {
		t.Errorf("expected 1 changed ref, got %+v", changed)
	}
	if m, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); !ok {
		t.Errorf("expected catalog to contain s1")
	} else if m.Title != "session s1" {
		t.Errorf("unexpected title %q", m.Title)
	}
}

// TestDelayedProviderDoesNotBlockCachedOrOtherProvider verifies that a slow
// provider does not block cached listing or another fast provider.
func TestDelayedProviderDoesNotBlockCachedOrOtherProvider(t *testing.T) {
	slow := providertest.NewFake(model.AgentCodex, "SlowCodex")
	slow.DetectionData = provider.Detection{Present: true}
	slow.ScanDelay = 200 * time.Millisecond
	slow.Sessions = []model.SessionMeta{refresherMeta(model.AgentCodex, "slow1", time.Now().UTC())}
	slow.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	fast := providertest.NewFake(model.AgentClaude, "FastClaude")
	fast.DetectionData = provider.Detection{Present: true}
	fast.Sessions = []model.SessionMeta{refresherMeta(model.AgentClaude, "fast1", time.Now().UTC())}
	fast.StateOverride = &provider.ScanState{Sources: map[string]provider.SourceState{}}

	r, db := newTestRefresher(t, slow, fast)

	// Pre-seed a cached session in DB and catalog to simulate cache-first startup
	cached := refresherMeta(model.AgentClaude, "cached1", time.Now().UTC().Add(-time.Hour))
	if err := db.CommitScan(context.Background(), model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{cached},
	}); err != nil {
		t.Fatalf("pre-seed failed: %v", err)
	}
	r.catalog.Apply(provider.ScanResult{Changed: []model.SessionMeta{cached}})

	// Verify cached session is immediately readable
	if all := r.catalog.All(); len(all) != 1 || all[0].Ref.ID != "cached1" {
		t.Fatalf("expected cached session available immediately, got %+v", all)
	}

	fastFinished := make(chan struct{})
	r.SetOnChanged(func(agent model.AgentID, changed, removed []model.SessionRef) {
		if agent == model.AgentClaude && len(changed) == 1 && changed[0].ID == "fast1" {
			close(fastFinished)
		}
	})

	bootstrapDone := make(chan struct{})
	go func() {
		defer close(bootstrapDone)
		_ = r.Bootstrap(context.Background())
	}()

	// Fast provider must complete well before slow provider finishes
	select {
	case <-fastFinished:
		// Succeeded: fast finished while slow is still delayed!
		// Verify catalog now has cached1 and fast1, but not yet slow1
		if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "fast1"}); !ok {
			t.Error("fast1 must be in catalog")
		}
		if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentCodex, ID: "slow1"}); ok {
			t.Error("slow1 should not yet be in catalog")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("fast provider did not complete within expected time")
	}

	// Wait for full bootstrap to finish
	<-bootstrapDone

	// Now slow1 must also be in catalog
	if _, ok := r.catalog.Get(model.SessionRef{Agent: model.AgentCodex, ID: "slow1"}); !ok {
		t.Error("slow1 must be in catalog after bootstrap completion")
	}
}

// TestBenchmarkWarmFirstPaint measures that index.Open -> LoadCatalog -> Catalog.Reset
// executes in under 300 ms for 1,000 cached sessions.
func TestBenchmarkWarmFirstPaint(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// 1. Prepare a cache database with 1,000 sessions
	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	metas := make([]model.SessionMeta, 1000)
	for i := range 1000 {
		metas[i] = model.SessionMeta{
			Ref: model.SessionRef{
				Agent: model.AgentClaude,
				ID:    fmt.Sprintf("bench-session-%04d", i),
			},
			Title:       fmt.Sprintf("Benchmarked Session %d", i),
			SourcePath:  fmt.Sprintf("/home/user/.claude/sessions/%04d.jsonl", i),
			CWD:         "/home/user/workspace/bench",
			RepoRoot:    "/home/user/workspace",
			CreatedAt:   now.Add(-time.Duration(i) * time.Minute),
			UpdatedAt:   now.Add(-time.Duration(i) * time.Second),
			Counts:      model.MessageCounts{User: 5, Assistant: 5, ToolCalls: 10},
			Tokens:      model.TokenUsage{Input: 1000, Output: 500, Reasoning: 200},
			CostUSD:     0.02,
			Model:       "claude-sonnet-5",
			FirstPrompt: "How do I optimize database startup?",
		}
	}

	err = db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: metas,
		State:   provider.ScanState{Cursor: "bench-cursor"},
	})
	if err != nil {
		t.Fatalf("CommitScan 1000 failed: %v", err)
	}
	_ = db.Close()

	// 2. Measure warm first paint path: Open -> LoadCatalog -> Catalog.Reset
	start := time.Now()

	warmDB, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("warm Open failed: %v", err)
	}
	defer func() { _ = warmDB.Close() }()

	cachedMetas, err := warmDB.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("warm LoadCatalog failed: %v", err)
	}

	cat := scan.NewCatalog()
	cat.Reset(cachedMetas)
	all := cat.All()

	elapsed := time.Since(start)

	if len(all) != 1000 {
		t.Fatalf("expected 1000 sessions loaded, got %d", len(all))
	}

	t.Logf("Warm first paint with 1,000 sessions: %v (target < 300ms; "+
		"advisory here — the plan requires measuring on a real ≈300 MB "+
		"history, not asserting machine-dependent CI timings)", elapsed)
}

// claudeScanFixture is a test helper replicating claude's fixtureRoot layout
// without importing the claude package (avoiding a test-only cycle).
func claudeScanFixture(t *testing.T, root, name, body string) string {
	t.Helper()
	project := filepath.Join(root, "projects", "project-x")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, name+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// minimalClaudeRecord builds a minimal valid Claude JSONL record line.
func minimalClaudeRecord(kind, text, uuid, parentID, stamp string) string {
	rec := map[string]any{
		"type":      kind,
		"uuid":      uuid,
		"timestamp": stamp,
		"cwd":       "/tmp/project-alpha",
		"message": map[string]any{
			"role":    kind,
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	}
	if parentID != "" {
		rec["parentUuid"] = parentID
	}
	if kind == "assistant" {
		rec["message"] = map[string]any{
			"id":    "msg_" + uuid,
			"model": "claude-sonnet-5",
			"usage": map[string]any{
				"input_tokens": 10, "output_tokens": 5,
			},
			"content": []map[string]any{{"type": "text", "text": text}},
		}
	}
	b, _ := json.Marshal(rec)
	return string(b) + "\n"
}

// TestRefresherAppendFixture exercises an append on the real claude provider:
// initial scan then appended JSONL increments counts exactly once.
func TestRefresherAppendFixture(t *testing.T) {
	root := t.TempDir()
	records := minimalClaudeRecord("user", "Hello", "u1", "", "2026-09-28T10:00:00Z")
	path := claudeScanFixture(t, root, "append_test", records)

	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.DetectionData = provider.Detection{Present: true}

	// Drive the real provider directly so we can exercise resumption.
	realProv := claude.New(root, nil)

	// We use Refresher with a wrapper that delegates to the real provider.
	// Since Refresher.Refresh calls p.Scan, we need the real provider; but we
	// also want a stable pointer for the test. Use the real one.
	r, db := newTestRefresher(t, realProv)

	// 1st scan: initial content.
	changed, _, err := r.Refresh(context.Background(), realProv)
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed ref, got %+v", changed)
	}

	// 2nd scan: unchanged, no changes reported.
	changed, removed, err := r.Refresh(context.Background(), realProv)
	if err != nil {
		t.Fatalf("unchanged Refresh: %v", err)
	}
	if len(changed) != 0 || len(removed) != 0 {
		t.Errorf("expected no changes on rescan, got changed=%+v removed=%+v", changed, removed)
	}

	// 3rd scan: append a new assistant message → counts change exactly once.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(minimalClaudeRecord("assistant", "Hi!", "a1", "u1", "2026-09-28T10:00:05Z")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	changed, _, err = r.Refresh(context.Background(), realProv)
	if err != nil {
		t.Fatalf("append Refresh: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed ref after append, got %+v", changed)
	}

	// Verify counts incremented exactly once.
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 session, got %+v", metas)
	}
	got := metas[0]
	if got.Counts.User != 1 || got.Counts.Assistant != 1 {
		t.Errorf("expected user=1 assistant=1 after append, got %+v", got.Counts)
	}
}

// TestRefresherShrinkFixture ensures file shrink triggers a full rescan with
// correct counts, not stale accumulated values.
func TestRefresherShrinkFixture(t *testing.T) {
	root := t.TempDir()
	full := minimalClaudeRecord("user", "Hello", "u1", "", "2026-09-28T10:00:00Z") +
		minimalClaudeRecord("assistant", "Hi!", "a1", "u1", "2026-09-28T10:00:05Z")
	path := claudeScanFixture(t, root, "shrink_test", full)

	realProv := claude.New(root, nil)
	r, db := newTestRefresher(t, realProv)

	if _, _, err := r.Refresh(context.Background(), realProv); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// Shrink the file to only the user record.
	shrunk := minimalClaudeRecord("user", "Hello", "u1", "", "2026-09-28T10:00:00Z")
	if err := os.WriteFile(path, []byte(shrunk), 0o600); err != nil {
		t.Fatal(err)
	}
	newTime := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	changed, _, err := r.Refresh(context.Background(), realProv)
	if err != nil {
		t.Fatalf("shrink Refresh: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed ref after shrink, got %+v", changed)
	}

	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 session, got %+v", metas)
	}
	got := metas[0]
	if got.Counts.User != 1 || got.Counts.Assistant != 0 {
		t.Errorf("expected user=1 assistant=0 after shrink, got %+v", got.Counts)
	}
	if got.Tokens != (model.TokenUsage{Input: 0, Output: 0}) {
		t.Errorf("expected zeroed tokens after shrink, got %+v", got.Tokens)
	}
}

// TestRefresherVanishedSourceRemoved ensures a source file that disappears
// produces a Removed ref in the next scan.
func TestRefresherVanishedSourceRemoved(t *testing.T) {
	root := t.TempDir()
	body := minimalClaudeRecord("user", "Hello", "u1", "", "2026-09-28T10:00:00Z")
	path := claudeScanFixture(t, root, "vanish_test", body)

	realProv := claude.New(root, nil)
	r, db := newTestRefresher(t, realProv)

	if _, _, err := r.Refresh(context.Background(), realProv); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// Remove the source.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	changed, removed, err := r.Refresh(context.Background(), realProv)
	if err != nil {
		t.Fatalf("vanish Refresh: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("expected no changes after vanish, got %+v", changed)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed ref, got %+v", removed)
	}

	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Errorf("expected empty catalog after vanish, got %+v", metas)
	}
}

// TestRefresherCanceledScanLeavesState ensures canceling mid-scan keeps the
// previous cache state intact (rollback semantics).
func TestRefresherCanceledScan(t *testing.T) {
	root := t.TempDir()
	_ = claudeScanFixture(t, root, "cancel_test",
		minimalClaudeRecord("user", "Hello", "u1", "", "2026-09-28T10:00:00Z"))

	realProv := claude.New(root, nil)
	r, db := newTestRefresher(t, realProv)

	if _, _, err := r.Refresh(context.Background(), realProv); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	// Run a refresh with a canceled context; scan should abort.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.Refresh(canceled, realProv); err == nil {
		t.Error("expected error from canceled Refresh, got nil")
	}

	// Verify catalog and state remain unchanged.
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Errorf("expected 1 session, got %+v", metas)
	}
	st, err := db.LoadState(context.Background(), model.AgentClaude)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Sources) != 1 {
		t.Errorf("expected 1 source state, got %+v", st.Sources)
	}
}
