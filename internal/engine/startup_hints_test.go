package engine

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestStartupWriterHintsBeforeVerification(t *testing.T) {
	for _, outcome := range []string{"verified", "rebuilt", "corrupt-check", "fallback", "canceled", "bad-json", "missing"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			roots := paths.Roots{Claude: filepath.Join(dir, "generated"), Cache: filepath.Join(dir, "cache")}
			key := index.RootsKey(roots.Claude, "", "")
			meta := model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "cached"}, CWD: "/generated/work"}
			if outcome != "missing" {
				db, err := index.Open(ctx, roots.Cache, key)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{Changed: []model.SessionMeta{meta}}); err != nil {
					t.Fatal(err)
				}
				if outcome == "corrupt-check" {
					// Metadata remains readable, but quick_check must reject this
					// CHECK violation and exercise the real integrity recovery path.
					if _, err := db.SQLDB().ExecContext(ctx, "PRAGMA ignore_check_constraints=ON; UPDATE sessions SET archived=2; PRAGMA ignore_check_constraints=OFF;"); err != nil {
						t.Fatal(err)
					}
				}
				if outcome == "bad-json" {
					if _, err := db.SQLDB().ExecContext(ctx, "UPDATE sessions SET meta_json = '{'"); err != nil {
						t.Fatal(err)
					}
				}
				_ = db.Close()
			}
			fake := providertest.NewFake(model.AgentClaude, "generated")
			fake.DetectionData = provider.Detection{Present: true}
			// Bootstrap is blocked independently so final publication is observable.
			watch := &startupWatchBarrier{Provider: fake, entered: make(chan struct{}), release: make(chan struct{})}
			entered, release := make(chan struct{}), make(chan struct{})
			catalogEvents := make(chan struct{}, 8)
			e := NewEngine(WithRoots(roots), WithService(NewService(scan.NewCatalog(), provider.Set{watch})),
				WithEmitter(EmitterFunc(func(name string, _ any) {
					if name == CatalogChangedEvent {
						select {
						case catalogEvents <- struct{}{}:
						default:
						}
					}
				})))
			e.writerOpen = func(ctx context.Context, dir, key string) (*index.DB, []model.SessionMeta, error) {
				close(entered)
				<-release
				if outcome == "fallback" {
					return nil, nil, errors.New("generated open failure")
				}
				if outcome == "canceled" {
					return nil, nil, ctx.Err()
				}
				if outcome == "rebuilt" {
					db, err := index.Open(ctx, dir, key)
					if err != nil {
						return nil, nil, err
					}
					if err := db.Rebuild(ctx); err != nil {
						_ = db.Close()
						return nil, nil, err
					}
					return db, nil, nil
				}
				return openWriter(ctx, dir, key)
			}
			finished := make(chan error, 1)
			go func() { finished <- e.Start(ctx) }()
			var once, watchOnce sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			unwatch := func() { watchOnce.Do(func() { close(watch.release) }) }
			defer func() {
				unblock()
				unwatch()
				<-finished
				_ = e.Close()
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("verification not reached")
			}
			want := 1
			if outcome == "bad-json" || outcome == "missing" {
				want = 0
			}
			rows, err := e.ListSessions(ctx, "", FilterOpts{}, SortOpts{})
			if err != nil || len(rows) != want {
				t.Fatalf("provisional rows=%d err=%v, want %d", len(rows), err, want)
			}
			if _, err := e.ListGroups(ctx, GroupMode("dir-agent"), FilterOpts{}); err != nil {
				t.Fatal(err)
			}
			counts, err := e.AgentCounts(ctx, FilterOpts{})
			if err != nil || counts[string(model.AgentClaude)] != want {
				t.Fatalf("provisional counts=%v err=%v", counts, err)
			}
			watched, changed := e.WatchDiscovery()
			select {
			case <-changed:
				t.Fatal("held verification unexpectedly changed discovery")
			default:
			}
			status, err := e.DiscoveryStatus(ctx)
			if watched != status {
				t.Fatalf("watch snapshot=%+v, status=%+v", watched, status)
			}
			if err != nil || status.Complete || status.CatalogUsable != (want == 1) {
				t.Fatalf("provisional status=%+v err=%v", status, err)
			}
			if want == 1 {
				select {
				case <-catalogEvents:
				case <-time.After(10 * time.Second):
					t.Fatal("cached catalog event waits for verification")
				}
			}
			if outcome == "canceled" {
				cancel()
			}
			unblock()
			if outcome != "fallback" && outcome != "canceled" {
				select {
				case <-watch.entered:
				case <-time.After(10 * time.Second):
					t.Fatal("verified startup did not reach watcher")
				}
			} else {
				// Start must finish clearing hints before fallback can proceed.
				if err := <-finished; err != nil {
					t.Fatal(err)
				}
				finished <- nil // cleanup owns the final receive
			}
			rows, err = e.ListSessions(t.Context(), "", FilterOpts{}, SortOpts{})
			want = 0
			if outcome == "verified" {
				want = 1
			}
			if err != nil || len(rows) != want {
				t.Fatalf("authoritative rows=%d err=%v, want %d", len(rows), err, want)
			}
		})
	}
}
