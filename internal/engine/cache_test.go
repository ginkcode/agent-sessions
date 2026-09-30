package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// testRoots are the provider roots of sharedEngine; testKey names its index.
var (
	testRoots = paths.Roots{Claude: "/nonexistent/claude"}
	testKey   = index.RootsKey(testRoots.Claude, testRoots.Codex, testRoots.OpenCodeData)
)

func sharedEngine(t *testing.T, dir string, fake *providertest.Fake) *Engine {
	t.Helper()
	return sharedEngineWithRoots(t, dir, testRoots, fake)
}

func sharedEngineWithRoots(t *testing.T, dir string, roots paths.Roots, fake *providertest.Fake) *Engine {
	t.Helper()
	fake.DetectionData = provider.Detection{Present: true}
	e := NewEngine(
		WithRoots(roots),
		WithService(NewService(scan.NewCatalog(), provider.Set{fake})),
		WithCacheEnabled(true),
		WithCacheDir(dir),
	)
	e.promoteEvery = 20 * time.Millisecond
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func claudeSession(id string) model.SessionMeta {
	return model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: id}, Title: id}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dbHas(db *index.DB, id string) bool {
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		return false
	}
	for _, m := range metas {
		if m.Ref.ID == id {
			return true
		}
	}
	return false
}

func TestSharedCache_ReaderSeedsFromWriterAndTakesOver(t *testing.T) {
	dir := t.TempDir()

	fa := providertest.NewFake(model.AgentClaude, "Claude")
	fa.Sessions = []model.SessionMeta{claudeSession("s1")}
	a := sharedEngine(t, dir, fa)
	if a.Refresher().DB() == nil {
		t.Fatal("first engine is not the writer")
	}
	waitFor(t, "writer commit", func() bool { return dbHas(a.Refresher().DB(), "s1") })

	// The reader's own first scan is slow, so s1 can only come from the
	// writer's database.
	fb := providertest.NewFake(model.AgentClaude, "Claude")
	fb.Sessions = []model.SessionMeta{claudeSession("s1"), claudeSession("s2")}
	fb.ScanDelay = 300 * time.Millisecond
	b := sharedEngine(t, dir, fb)
	if b.Refresher().DB() != nil {
		t.Fatal("second engine opened the database for writing")
	}
	if _, ok := b.Service().Catalog().Get(claudeSession("s1").Ref); !ok {
		t.Error("reader catalog not seeded from the writer's database")
	}
	if db, reader := b.searchDB(); db == nil || !reader {
		t.Error("reader has no handle to search the writer's index")
	}
	if _, err := b.Search(t.Context(), "s1", index.SearchFilter{}); err != nil {
		t.Errorf("reader search: %v", err)
	}
	waitFor(t, "reader scan", func() bool {
		_, ok := b.Service().Catalog().Get(claudeSession("s2").Ref)
		return ok
	})
	if dbHas(a.Refresher().DB(), "s2") {
		t.Error("reader wrote to the database")
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "promotion", func() bool {
		r := b.Refresher()
		return r != nil && r.DB() != nil
	})
	waitFor(t, "new writer commit", func() bool { return dbHas(b.Refresher().DB(), "s2") })
	if _, ok := b.Service().Catalog().Get(claudeSession("s1").Ref); !ok {
		t.Error("catalog lost s1 on promotion")
	}
	if _, err := index.TryLock(dir, testKey); !errors.Is(err, index.ErrLocked) {
		t.Errorf("lock after promotion: err = %v, want ErrLocked", err)
	}

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := index.TryLock(dir, testKey)
	if err != nil {
		t.Fatalf("lock after both closed: %v", err)
	}
	_ = l.Unlock()
}

// A reader that starts before any writer created the database scans
// everything itself and picks the database up once it exists.
func TestSharedCache_ReaderOpensDatabaseLater(t *testing.T) {
	dir := t.TempDir()
	lock, err := index.TryLock(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	fb := providertest.NewFake(model.AgentClaude, "Claude")
	fb.Sessions = []model.SessionMeta{claudeSession("s1")}
	b := sharedEngine(t, dir, fb)
	if db, _ := b.searchDB(); db != nil {
		t.Fatal("reader has a database before one exists")
	}
	waitFor(t, "reader scan", func() bool {
		_, ok := b.Service().Catalog().Get(claudeSession("s1").Ref)
		return ok
	})

	db, err := index.Open(t.Context(), dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "read-only open", func() bool { db, _ := b.searchDB(); return db != nil })
	_ = db.Close()
	_ = lock.Unlock()
	waitFor(t, "promotion", func() bool {
		r := b.Refresher()
		return r != nil && r.DB() != nil
	})
}

func TestMergeCatalog(t *testing.T) {
	db := []model.SessionMeta{claudeSession("a"), claudeSession("b")}
	own := []model.SessionMeta{claudeSession("b"), claudeSession("c")}
	own[0].Live, own[0].Title = true, "b, rescanned"
	got := mergeCatalog(db, own)
	if len(got) != 3 {
		t.Fatalf("merged %d sessions, want 3: %+v", len(got), got)
	}
	if got[0].Ref.ID != "a" || got[1].Title != "b, rescanned" || !got[1].Live || got[2].Ref.ID != "c" {
		t.Errorf("mergeCatalog = %+v", got)
	}
}

// Engines that scan different provider roots each write their own index:
// neither is seeded with, nor searches, the other's sessions.
func TestSharedCache_DifferentRootsDoNotShare(t *testing.T) {
	dir := t.TempDir()
	fa := providertest.NewFake(model.AgentClaude, "Claude")
	fa.Sessions = []model.SessionMeta{claudeSession("mine")}
	a := sharedEngine(t, dir, fa)

	fb := providertest.NewFake(model.AgentClaude, "Claude")
	fb.Sessions = []model.SessionMeta{claudeSession("theirs")}
	b := sharedEngineWithRoots(t, dir, paths.Roots{Claude: "/nonexistent/other"}, fb)

	for name, e := range map[string]*Engine{"a": a, "b": b} {
		if r := e.Refresher(); r == nil || r.DB() == nil {
			t.Fatalf("engine %s is not the writer of its own index", name)
		}
	}
	waitFor(t, "both commit", func() bool {
		return dbHas(a.Refresher().DB(), "mine") && dbHas(b.Refresher().DB(), "theirs")
	})
	if dbHas(a.Refresher().DB(), "theirs") || dbHas(b.Refresher().DB(), "mine") {
		t.Error("an index holds the other roots' session")
	}
	if _, ok := b.Service().Catalog().Get(claudeSession("mine").Ref); ok {
		t.Error("catalog has the other roots' session")
	}
}

// A reader's search leaves out a session it has already removed, even
// while the writer's index still has it.
func TestSharedCache_ReaderSearchSkipsRemoved(t *testing.T) {
	dir := t.TempDir()
	s1 := claudeSession("s1")
	s1.Title = "needle"
	transcripts := map[string]*model.Transcript{"s1": {Meta: s1}}
	fa := providertest.NewFake(model.AgentClaude, "Claude")
	fa.Sessions, fa.Transcripts = []model.SessionMeta{s1}, transcripts
	a := sharedEngine(t, dir, fa)
	waitFor(t, "writer commit", func() bool { return dbHas(a.Refresher().DB(), "s1") })

	fb := providertest.NewFake(model.AgentClaude, "Claude")
	fb.Sessions, fb.Transcripts = []model.SessionMeta{s1}, transcripts
	b := sharedEngine(t, dir, fb)
	waitFor(t, "title indexed", func() bool {
		hits, err := b.Search(t.Context(), "needle", index.SearchFilter{})
		return err == nil && len(hits) > 0
	})
	b.Forget(t.Context(), []model.SessionRef{s1.Ref})
	hits, err := b.Search(t.Context(), "needle", index.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("reader search returned a removed session: %+v", hits)
	}
}
