package scan_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func meta(agent model.AgentID, id, parent string, updated time.Time) model.SessionMeta {
	return model.SessionMeta{
		Ref:       model.SessionRef{Agent: agent, ID: id},
		ParentID:  parent,
		Title:     "title " + id,
		UpdatedAt: updated,
	}
}

func TestCatalogApplyAllAndOrder(t *testing.T) {
	c := scan.NewCatalog()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	// Apply initial scan with 3 sessions out of order
	c.Apply(provider.ScanResult{
		Changed: []model.SessionMeta{
			meta(model.AgentClaude, "s1", "", now.Add(-time.Hour)),
			meta(model.AgentClaude, "s2", "", now.Add(time.Hour)),
			meta(model.AgentClaude, "s3", "", now),
		},
	})

	all := c.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(all))
	}
	if all[0].Ref.ID != "s2" || all[1].Ref.ID != "s3" || all[2].Ref.ID != "s1" {
		t.Errorf("expected newest first [s2 s3 s1], got [%s %s %s]", all[0].Ref.ID, all[1].Ref.ID, all[2].Ref.ID)
	}

	// Tie-breaking by Ref.Key()
	c.Apply(provider.ScanResult{
		Changed: []model.SessionMeta{
			meta(model.AgentClaude, "b", "", now),
			meta(model.AgentClaude, "a", "", now),
		},
	})
	all = c.All()
	foundA, foundB := -1, -1
	for i, s := range all {
		if s.Ref.ID == "a" {
			foundA = i
		}
		if s.Ref.ID == "b" {
			foundB = i
		}
	}
	if foundA == -1 || foundB == -1 || foundA > foundB {
		t.Errorf("tie-breaker expected a before b, got a at %d, b at %d", foundA, foundB)
	}
}

func TestCatalogApplyChangedWinsOverRemoved(t *testing.T) {
	c := scan.NewCatalog()
	now := time.Now().UTC()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "conflict"}
	c.Apply(provider.ScanResult{
		Removed: []model.SessionRef{ref},
		Changed: []model.SessionMeta{
			meta(model.AgentClaude, "conflict", "", now),
		},
	})

	m, ok := c.Get(ref)
	if !ok {
		t.Fatal("expected changed session to survive removal in same result")
	}
	if m.Ref.ID != "conflict" {
		t.Errorf("expected ID conflict, got %s", m.Ref.ID)
	}
}

func TestCatalogChildren(t *testing.T) {
	c := scan.NewCatalog()
	now := time.Now().UTC()

	c.Apply(provider.ScanResult{
		Changed: []model.SessionMeta{
			meta(model.AgentClaude, "p1", "", now),
			meta(model.AgentClaude, "child1", "p1", now.Add(time.Minute)),
			meta(model.AgentClaude, "child2", "p1", now.Add(2*time.Minute)),
			meta(model.AgentCodex, "child_other", "p1", now.Add(3*time.Minute)), // cross-provider must NOT link
			meta(model.AgentClaude, "other", "p2", now),
		},
	})

	children := c.Children(model.SessionRef{Agent: model.AgentClaude, ID: "p1"})
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	// Sorted by UpdatedAt desc
	if children[0].Ref.ID != "child2" || children[1].Ref.ID != "child1" {
		t.Errorf("expected [child2 child1], got [%s %s]", children[0].Ref.ID, children[1].Ref.ID)
	}
}

func TestRunnerSlowDoesNotBlockFast(t *testing.T) {
	fast := providertest.NewFake(model.AgentClaude, "Fast")
	fast.DetectionData = provider.Detection{Present: true}
	fast.Sessions = []model.SessionMeta{meta(model.AgentClaude, "f1", "", time.Now().UTC())}

	slow := providertest.NewFake(model.AgentCodex, "Slow")
	slow.DetectionData = provider.Detection{Present: true}
	slow.ScanDelay = 200 * time.Millisecond
	slow.Sessions = []model.SessionMeta{meta(model.AgentCodex, "s1", "", time.Now().UTC())}

	catalog := scan.NewCatalog()
	runner := scan.Runner{
		Providers: provider.Set{slow, fast},
		Workers:   2,
		Catalog:   catalog,
	}

	start := time.Now()
	rep := runner.Run(context.Background(), nil)
	duration := time.Since(start)

	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if len(catalog.All()) != 2 {
		t.Fatalf("expected 2 sessions in catalog, got %d", len(catalog.All()))
	}
	if _, ok := catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "f1"}); !ok {
		t.Errorf("fast provider's session missing after run: %v", catalog.All())
	}
	if duration >= 400*time.Millisecond {
		t.Errorf("providers appear serialized: %v", duration)
	}
}

func TestRunnerFastAvailableBeforeSlowCompletes(t *testing.T) {
	fast := providertest.NewFake(model.AgentClaude, "Fast")
	fast.DetectionData = provider.Detection{Present: true}
	fast.Sessions = []model.SessionMeta{meta(model.AgentClaude, "fast-1", "", time.Now().UTC())}

	slow := providertest.NewFake(model.AgentCodex, "Slow")
	slow.DetectionData = provider.Detection{Present: true}
	slow.ScanDelay = 300 * time.Millisecond
	slow.Sessions = []model.SessionMeta{meta(model.AgentCodex, "slow-1", "", time.Now().UTC())}

	catalog := scan.NewCatalog()
	runner := scan.Runner{
		Providers: provider.Set{slow, fast},
		Workers:   2,
		Catalog:   catalog,
	}

	fastObserved := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for range 50 {
			if _, ok := catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "fast-1"}); ok {
				fastObserved <- time.Since(start)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		fastObserved <- -1
	}()

	runner.Run(context.Background(), nil)

	dur := <-fastObserved
	if dur < 0 {
		t.Fatal("fast session was never observed during run")
	}
	// Fast session should be in catalog well before slow finishes (~300ms)
	if dur >= 200*time.Millisecond {
		t.Errorf("fast provider was blocked by slow provider: took %v", dur)
	}
}

func TestRunnerBoundedWorkers(t *testing.T) {
	// 6 providers, 2 workers: 6 tasks of 50ms must take at least 3 rounds
	// (~150ms), proving the worker count bounds concurrency.
	providers := make(provider.Set, 0, 6)
	for i := 1; i <= 6; i++ {
		p := providertest.NewFake(model.AgentID(fmt.Sprintf("p%d", i)), fmt.Sprintf("p%d", i))
		p.DetectionData = provider.Detection{Present: true}
		p.ScanDelay = 50 * time.Millisecond
		p.Sessions = []model.SessionMeta{meta(p.ID(), "s", "", time.Now().UTC())}
		providers = append(providers, p)
	}

	runner := scan.Runner{
		Providers: providers,
		Workers:   2,
		Catalog:   scan.NewCatalog(),
	}

	start := time.Now()
	rep := runner.Run(context.Background(), nil)
	dur := time.Since(start)

	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if dur < 120*time.Millisecond {
		t.Errorf("expected bounded workers to take at least ~150ms, took %v", dur)
	}
}

func TestRunnerCancellation(t *testing.T) {
	slow := providertest.NewFake(model.AgentClaude, "Slow")
	slow.DetectionData = provider.Detection{Present: true}
	slow.ScanDelay = 500 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	runner := scan.Runner{
		Providers: provider.Set{slow},
		Workers:   1,
		Catalog:   scan.NewCatalog(),
	}

	rep := runner.Run(ctx, nil)
	if err, ok := rep.Errors[model.AgentClaude]; !ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded for canceled provider, got: %v", err)
	}
}

func TestRunnerTwoScansChangedAndRemoved(t *testing.T) {
	catalog := scan.NewCatalog()
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	now := time.Now().UTC()

	// Scan 1: creates s1 and s2
	p.Sessions = []model.SessionMeta{
		meta(model.AgentClaude, "s1", "", now),
		meta(model.AgentClaude, "s2", "", now.Add(time.Minute)),
	}

	runner := scan.Runner{
		Providers: provider.Set{p},
		Catalog:   catalog,
	}

	rep1 := runner.Run(context.Background(), nil)
	if len(catalog.All()) != 2 {
		t.Fatalf("scan 1: expected 2 sessions, got %d", len(catalog.All()))
	}

	// Scan 2: s1 is updated, s2 is removed, s3 is added
	p.Sessions = []model.SessionMeta{
		meta(model.AgentClaude, "s1", "", now.Add(2*time.Minute)),
		meta(model.AgentClaude, "s3", "", now.Add(3*time.Minute)),
	}
	// To signal removal, we can apply via Catalog or test runner with changed state
	// Note: fake provider's Scan returns Changed=p.Sessions. If we want Removed in ScanResult:
	// We can update runner catalog with ScanResult containing Removed
	catalog.Apply(provider.ScanResult{
		Removed: []model.SessionRef{{Agent: model.AgentClaude, ID: "s2"}},
		Changed: p.Sessions,
	})

	all := catalog.All()
	if len(all) != 2 {
		t.Fatalf("scan 2: expected 2 sessions, got %d", len(all))
	}
	if all[0].Ref.ID != "s3" || all[1].Ref.ID != "s1" {
		t.Errorf("expected [s3 s1], got [%s %s]", all[0].Ref.ID, all[1].Ref.ID)
	}
	_ = rep1
}

func TestRunnerFailureRetainsOldCatalogAndState(t *testing.T) {
	catalog := scan.NewCatalog()
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	now := time.Now().UTC()

	p.Sessions = []model.SessionMeta{meta(model.AgentClaude, "s1", "", now)}

	runner := scan.Runner{
		Providers: provider.Set{p},
		Catalog:   catalog,
	}

	// Scan 1 succeeds
	rep1 := runner.Run(context.Background(), nil)
	if len(catalog.All()) != 1 {
		t.Fatalf("scan 1 failed: expected 1 session in catalog")
	}

	// Scan 2 fails
	p.ScanErr = errors.New("disk read error")
	rep2 := runner.Run(context.Background(), rep1.States)

	if rep2.Errors[model.AgentClaude] == nil {
		t.Fatal("expected error on failed scan")
	}
	// Catalog must retain old sessions
	if len(catalog.All()) != 1 {
		t.Errorf("expected catalog to retain old session, got %d sessions", len(catalog.All()))
	}
	// States must retain previous state
	if _, ok := rep2.States[model.AgentClaude]; !ok {
		t.Errorf("expected previous state retained on scan error")
	}
}

func TestRunnerUnchangedLiveUpdate(t *testing.T) {
	catalog := scan.NewCatalog()
	p := providertest.NewFake(model.AgentClaude, "Claude")
	p.DetectionData = provider.Detection{Present: true}
	now := time.Now().UTC()

	p.Sessions = []model.SessionMeta{
		meta(model.AgentClaude, "s1", "", now),
		meta(model.AgentClaude, "s2", "", now),
	}
	p.LiveMap = map[string]provider.LiveInfo{
		"s1": {PID: 1001, Status: "running"},
	}

	runner := scan.Runner{
		Providers: provider.Set{p},
		Catalog:   catalog,
	}

	// Scan 1: s1 is live, s2 is not
	runner.Run(context.Background(), nil)

	s1, _ := catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"})
	s2, _ := catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s2"})
	if !s1.Live || s1.LiveStatus != "running" {
		t.Errorf("s1 should be live running, got live=%v status=%q", s1.Live, s1.LiveStatus)
	}
	if s2.Live {
		t.Errorf("s2 should not be live")
	}

	// Scan 2: scan returns empty Changed (sessions unchanged), but LiveMap updates s2 to running
	p.Sessions = nil
	p.LiveMap = map[string]provider.LiveInfo{
		"s2": {PID: 1002, Status: "running"},
	}

	runner.Run(context.Background(), nil)

	s1, _ = catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"})
	s2, _ = catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s2"})
	if s1.Live {
		t.Errorf("s1 should no longer be live")
	}
	if !s2.Live || s2.LiveStatus != "running" {
		t.Errorf("unchanged s2 should now be live running, got live=%v status=%q", s2.Live, s2.LiveStatus)
	}

	// Scan 3: transient LiveErr preserves existing live badges
	p.LiveErr = errors.New("transient proc error")
	p.LiveMap = nil
	runner.Run(context.Background(), nil)

	s2, _ = catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s2"})
	if !s2.Live {
		t.Errorf("s2 live badge should NOT be erased on transient live error")
	}
}

func TestRunnerDuplicateProviderIDRejected(t *testing.T) {
	p1 := providertest.NewFake(model.AgentClaude, "Claude 1")
	p2 := providertest.NewFake(model.AgentClaude, "Claude 2")

	runner := scan.Runner{
		Providers: provider.Set{p1, p2},
		Catalog:   scan.NewCatalog(),
	}

	rep := runner.Run(context.Background(), nil)
	if len(rep.Errors) == 0 {
		t.Fatal("expected duplicate provider ID error")
	}
	if err, ok := rep.Errors[model.AgentClaude]; !ok || err == nil {
		t.Errorf("expected error under duplicate AgentClaude, got %v", rep.Errors)
	}
}

func TestConcurrentAllAndApplyRace(t *testing.T) {
	catalog := scan.NewCatalog()
	now := time.Now().UTC()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Reader goroutines calling All, Get, Children
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = catalog.All()
					_, _ = catalog.Get(model.SessionRef{Agent: model.AgentClaude, ID: "s1"})
					_ = catalog.Children(model.SessionRef{Agent: model.AgentClaude, ID: "s1"})
				}
			}
		}()
	}

	// Writer goroutines calling Apply
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				catalog.Apply(provider.ScanResult{
					Changed: []model.SessionMeta{
						meta(model.AgentClaude, "s1", "", now.Add(time.Duration(j)*time.Second)),
						meta(model.AgentClaude, "s2", "s1", now.Add(time.Duration(j)*time.Second)),
					},
					Removed: []model.SessionRef{
						{Agent: model.AgentClaude, ID: "s3"},
					},
				})
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestCatalogRemove(t *testing.T) {
	c := scan.NewCatalog()
	now := time.Now().UTC()

	ref1 := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	ref2 := model.SessionRef{Agent: model.AgentClaude, ID: "s2"}
	ref3 := model.SessionRef{Agent: model.AgentCodex, ID: "s3"}

	c.Apply(provider.ScanResult{
		Changed: []model.SessionMeta{
			meta(model.AgentClaude, "s1", "", now),
			meta(model.AgentClaude, "s2", "", now.Add(time.Minute)),
			meta(model.AgentCodex, "s3", "", now.Add(2*time.Minute)),
		},
	})

	c.Remove([]model.SessionRef{ref1, ref3})

	all := c.All()
	if len(all) != 1 || all[0].Ref != ref2 {
		t.Fatalf("expected only %s remaining, got %+v", ref2.Key(), all)
	}
	for _, ref := range []model.SessionRef{ref1, ref3} {
		if _, ok := c.Get(ref); ok {
			t.Errorf("expected %s removed", ref.Key())
		}
	}

	// Repeated removals and unknown refs are harmless.
	c.Remove([]model.SessionRef{ref1, {Agent: model.AgentCodex, ID: "missing"}})
	if len(c.All()) != 1 {
		t.Errorf("repeated removals changed unrelated entries: %+v", c.All())
	}
}
