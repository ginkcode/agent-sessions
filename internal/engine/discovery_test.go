package engine

import (
	"context"
	"errors"
	"os"
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

func discoveryEvents() (Emitter, <-chan DiscoveryStatus) {
	ch := make(chan DiscoveryStatus, 100)
	return EmitterFunc(func(name string, p any) {
		if name == DiscoveryStatusEvent {
			ch <- p.(DiscoveryStatus)
		}
	}), ch
}

func awaitDiscovery(t *testing.T, ch <-chan DiscoveryStatus, match func(DiscoveryStatus) bool) DiscoveryStatus {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-ch:
			if match(s) {
				return s
			}
		case <-timer.C:
			t.Fatal("discovery barrier timeout")
			return DiscoveryStatus{}
		}
	}
}

func TestDiscoveryTerminalOutcomes(t *testing.T) {
	for _, mode := range []string{"disabled", "writer", "reader", "fallback"} {
		for _, outcome := range []string{"empty", "absent", "detect_failure", "scan_failure", "all_failed", "none"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				good := providertest.NewFake(model.AgentClaude, "generated")
				good.DetectionData.Present = outcome != "absent"
				bad := providertest.NewFake(model.AgentCodex, "generated")
				bad.DetectionData.Present = true
				provs := provider.Set{good}
				if outcome == "detect_failure" {
					bad.DetectErr = errors.New("generated failure")
					provs = append(provs, bad)
				}
				if outcome == "scan_failure" {
					bad.ScanErr = errors.New("generated failure")
					provs = append(provs, bad)
				}
				if outcome == "all_failed" {
					good.ScanErr = errors.New("generated failure")
				}
				if outcome == "none" {
					provs = nil
				}
				root := t.TempDir()
				roots := paths.Roots{Claude: filepath.Join(root, "generated"), Cache: filepath.Join(root, "cache")}
				if mode == "fallback" {
					if err := os.WriteFile(roots.Cache, []byte("generated"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "reader" {
					lock, err := index.TryLock(roots.Cache, index.RootsKey(roots.Claude, "", ""))
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = lock.Unlock() }()
				}
				emitter, events := discoveryEvents()
				e := NewEngine(WithRoots(roots), WithCacheEnabled(mode != "disabled"), WithService(NewService(scan.NewCatalog(), provs)), WithEmitter(emitter))
				defer func() { _ = e.Close() }()
				if err := e.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				s := awaitDiscovery(t, events, func(s DiscoveryStatus) bool { return s.Complete })
				want := DiscoverySettled
				failed := 0
				if outcome == "detect_failure" || outcome == "scan_failure" {
					want = DiscoveryDegraded
					failed = 1
				}
				if outcome == "all_failed" {
					want = DiscoveryFailed
					failed = 1
				}
				detected := 1
				if outcome == "absent" || outcome == "none" {
					detected = 0
				}
				if outcome == "scan_failure" {
					detected = 2
				}
				if s.State != want || !s.CatalogUsable || !s.DetectionComplete || s.DetectedProviders != detected || s.FailedProviders != failed {
					t.Fatalf("status=%+v", s)
				}
				wantMode := mode
				if outcome == "none" && mode != "disabled" {
					wantMode = "none"
				}
				if s.CacheMode != wantMode {
					t.Fatalf("mode=%s want=%s", s.CacheMode, wantMode)
				}
			})
		}
	}
}

func TestDiscoveryLifetimeAndAttempts(t *testing.T) {
	emitter, events := discoveryEvents()
	e := NewEngine(WithService(NewService(scan.NewCatalog(), nil)), WithEmitter(emitter))
	old := e.beginDiscovery(t.Context())
	old = e.discoveryReconciling(old, DiscoveryCacheReader)
	_ = e.Close()
	next := e.beginDiscovery(t.Context())
	next = e.discoveryReconciling(next, DiscoveryCacheReader)
	// Hold the equivalent of a late callback until the next lifetime exists.
	e.finishDiscovery(old, newDiscoveryAttempt(), nil)
	s, _ := e.DiscoveryStatus(t.Context())
	if s.Complete || s.Lifetime != 2 {
		t.Fatalf("late lifetime=%+v", s)
	}
	newer := e.discoveryReconciling(next, DiscoveryCacheWriter)
	e.finishDiscovery(next, newDiscoveryAttempt(), nil)
	s, _ = e.DiscoveryStatus(t.Context())
	if s.Complete || s.CacheMode != "writer" {
		t.Fatalf("late attempt=%+v", s)
	}
	e.finishDiscovery(newer, newDiscoveryAttempt(), nil)
	s = awaitDiscovery(t, events, func(s DiscoveryStatus) bool { return s.Lifetime == 2 && s.Complete })
	if s.State != DiscoverySettled {
		t.Fatal(s)
	}
}

type discoveryScanBarrier struct {
	provider.Provider
	entered chan struct{}
	once    sync.Once
}

// Scan holds the first scan until its owner cancels it: a deterministic
// barrier for Close, not a sleep.
func (p *discoveryScanBarrier) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	first := false
	p.once.Do(func() { first = true; close(p.entered) })
	if first {
		<-ctx.Done()
		return provider.ScanResult{}, ctx.Err()
	}
	return p.Provider.Scan(ctx, prev)
}

func TestDiscoveryCloseCancelsOwnedBootstrapAndRestarts(t *testing.T) {
	for _, cache := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "writer"}[cache], func(t *testing.T) {
			fake := providertest.NewFake(model.AgentClaude, "generated")
			fake.DetectionData.Present = true
			p := &discoveryScanBarrier{Provider: fake, entered: make(chan struct{})}
			root := t.TempDir()
			emitter, events := discoveryEvents()
			e := NewEngine(WithRoots(paths.Roots{Claude: filepath.Join(root, "generated"), Cache: filepath.Join(root, "cache")}),
				WithCacheEnabled(cache), WithService(NewService(scan.NewCatalog(), provider.Set{p})), WithEmitter(emitter))
			if err := e.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			<-p.entered
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			// Close waited for the cancelled bootstrap; its late finish was dropped.
			s, _ := e.DiscoveryStatus(t.Context())
			if s.Lifetime != 1 || s.State != DiscoveryFailed || !s.Complete {
				t.Fatalf("closed status=%+v", s)
			}
		drain:
			for {
				select {
				case ev := <-events:
					if ev.Complete {
						t.Fatalf("late terminal event=%+v", ev)
					}
				default:
					break drain
				}
			}
			if err := e.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = e.Close() }()
			s = awaitDiscovery(t, events, func(s DiscoveryStatus) bool { return s.Complete })
			if s.Lifetime != 2 || s.State != DiscoverySettled || s.DetectedProviders != 1 {
				t.Fatalf("restart=%+v", s)
			}
		})
	}
}

type discoveryReleaseBarrier struct {
	provider.Provider
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *discoveryReleaseBarrier) Detect(ctx context.Context) (provider.Detection, error) {
	first := false
	p.once.Do(func() { first = true; close(p.entered) })
	if first {
		<-p.release
	}
	return p.Provider.Detect(ctx)
}

// The reader's discovery attempt is held while it is promoted. The writer's
// attempt supersedes it; the reader's late result cannot overwrite it.
func TestDiscoveryReaderPromotionSupersedesReaderAttempt(t *testing.T) {
	root := t.TempDir()
	roots := paths.Roots{Claude: filepath.Join(root, "generated"), Cache: filepath.Join(root, "cache")}
	lock, err := index.TryLock(roots.Cache, index.RootsKey(roots.Claude, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	fake := providertest.NewFake(model.AgentClaude, "generated")
	fake.DetectionData.Present = true
	fake.Sessions = []model.SessionMeta{claudeSession("generated")}
	p := &discoveryReleaseBarrier{Provider: fake, entered: make(chan struct{}), release: make(chan struct{})}
	emitter, events := discoveryEvents()
	e := NewEngine(WithRoots(roots), WithService(NewService(scan.NewCatalog(), provider.Set{p})), WithEmitter(emitter))
	e.promoteEvery = time.Millisecond
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-p.entered
	if s, _ := e.DiscoveryStatus(t.Context()); s.CacheMode != DiscoveryCacheReader || s.Complete {
		t.Fatalf("held reader=%+v", s)
	}
	_ = lock.Unlock()
	w := awaitDiscovery(t, events, func(s DiscoveryStatus) bool {
		return s.CacheMode == DiscoveryCacheWriter && s.Complete
	})
	if w.State != DiscoverySettled || w.DetectedProviders != 1 {
		t.Fatalf("writer=%+v", w)
	}
	close(p.release)
	if err := e.Close(); err != nil { // waits for the held reader bootstrap
		t.Fatal(err)
	}
	if s, _ := e.DiscoveryStatus(t.Context()); s != w {
		t.Fatalf("late reader result: %+v, want %+v", s, w)
	}
	for {
		select {
		case ev := <-events:
			t.Fatalf("late event %+v", ev)
		default:
			return
		}
	}
}

func TestDiscoveryCancelledAttemptPublishesTerminalFailure(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "generated")
	fake.DetectionData.Present = true
	p := &discoveryScanBarrier{Provider: fake, entered: make(chan struct{})}
	emitter, events := discoveryEvents()
	e := NewEngine(WithCacheEnabled(false), WithService(NewService(scan.NewCatalog(), provider.Set{p})), WithEmitter(emitter))
	defer func() { _ = e.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-p.entered
	cancel()
	s := awaitDiscovery(t, events, func(s DiscoveryStatus) bool { return s.Complete })
	if s.State != DiscoveryFailed || !s.CatalogUsable || s.DetectedProviders != 1 {
		t.Fatalf("cancelled attempt=%+v", s)
	}
	got, _ := e.DiscoveryStatus(t.Context())
	if got != s {
		t.Fatalf("snapshot=%+v event=%+v", got, s)
	}
}
