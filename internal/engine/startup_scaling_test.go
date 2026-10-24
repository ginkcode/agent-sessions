package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// Fixture construction, seeding and Close are outside the observed interval.
func TestStartupFanout(t *testing.T) {
	if os.Getenv("AS_STARTUP_BASELINE") != "1" {
		t.Skip("set AS_STARTUP_BASELINE=1")
	}
	// Leaf directories are empty and grouped 100 per bucket below the Claude
	// projects watch root; session rows and source bytes stay equal per tier.
	for _, dirs := range []int{0, 1000, 10000} {
		t.Run(fmt.Sprint(dirs), func(t *testing.T) {
			_, roots := startupGeneratedFixture(t)
			for i := 0; i < dirs; i++ {
				if err := os.MkdirAll(filepath.Join(roots.Claude, "projects", "fanout", fmt.Sprintf("bucket-%02d", i/100), fmt.Sprintf("leaf-%05d", i)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			seed := NewEngine(WithRoots(roots))
			runStartupTrial(t, seed, false)
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			metrics := map[string][]float64{}
			var sums [5]int64
			for range 9 {
				e := NewEngine(WithRoots(roots))
				samples, work := runStartupTrial(t, e, true)
				for _, s := range samples {
					metrics[s.Phase+"_duration"] = append(metrics[s.Phase+"_duration"], float64(s.Finished-s.Started)/float64(time.Millisecond))
					metrics[s.Phase+"_end"] = append(metrics[s.Phase+"_end"], float64(s.Finished)/float64(time.Millisecond))
				}
				sums[0] += work.SourceOpens.Load()
				sums[1] += work.Records.Load()
				sums[2] += work.RecordBytes.Load()
				sums[3] += work.CheckpointDecodes.Load()
				sums[4] += work.CheckpointEncodes.Load()
				if err := e.Close(); err != nil {
					t.Fatal(err)
				}
			}
			logStartupMetrics(t, metrics)
			t.Logf("extra leaf directories=%d buckets=%d; scan work=%v", dirs, (dirs+99)/100, sums)
		})
	}
}

// Timed artificial latency is evidence of notification coupling, not a claim
// about real provider scan speed. Correctness tests use channels instead.
func TestStartupProviderPublication(t *testing.T) {
	if os.Getenv("AS_STARTUP_BASELINE") != "1" {
		t.Skip("set AS_STARTUP_BASELINE=1")
	}
	for _, mode := range []string{"disabled", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			metrics := map[string][]float64{}
			for range 9 {
				root := t.TempDir()
				cache := filepath.Join(root, "unusable")
				if err := os.WriteFile(cache, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				fast := providertest.NewFake(model.AgentClaude, "generated-fast")
				slow := providertest.NewFake(model.AgentCodex, "generated-slow")
				for _, p := range []*providertest.Fake{fast, slow} {
					p.DetectionData.Present = true
					p.Sessions = []model.SessionMeta{{Ref: model.SessionRef{Agent: p.ID(), ID: "generated"}}}
				}
				slow.ScanDelay = 300 * time.Millisecond
				events := make(chan time.Duration, 10)
				var o *StartupObservation
				e := NewEngine(WithCacheEnabled(mode != "disabled"), WithCacheDir(cache), WithService(NewService(scan.NewCatalog(), provider.Set{fast, slow})), WithEmitter(EmitterFunc(func(name string, _ any) {
					if name == CatalogChangedEvent {
						events <- time.Since(o.origin)
					}
				})))
				// Engine construction is excluded; the origin immediately precedes Start.
				o = NewStartupObservation()
				ctx := WithStartupObservation(context.Background(), o)
				if err := e.Start(ctx); err != nil {
					t.Fatal(err)
				}
				deadline := time.NewTimer(10 * time.Second)
				tick := time.NewTicker(time.Millisecond)
				var first time.Duration
				finished := false
				for !finished {
					rows, err := e.ListSessions(ctx, "", FilterOpts{}, SortOpts{})
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) > 0 && first == 0 {
						first = time.Since(o.origin)
					}
					for _, s := range o.Samples() {
						if s.Phase == "bootstrap" {
							finished = true
						}
					}
					if !finished {
						select {
						case <-deadline.C:
							t.Fatal("bootstrap timeout")
						case <-tick.C:
						}
					}
				}
				select {
				case event := <-events:
					metrics["first_catalog_event_end"] = append(metrics["first_catalog_event_end"], float64(event)/float64(time.Millisecond))
				case <-deadline.C:
					t.Fatal("event timeout")
				}
				tick.Stop()
				deadline.Stop()
				rows, _ := e.ListSessions(ctx, "", FilterOpts{}, SortOpts{})
				if len(rows) != 2 {
					t.Fatalf("final rows=%d", len(rows))
				}
				metrics["first_nonempty_query_end"] = append(metrics["first_nonempty_query_end"], float64(first)/float64(time.Millisecond))
				for _, s := range o.Samples() {
					if s.Phase == "start" || s.Phase == "bootstrap" {
						metrics[s.Phase+"_end"] = append(metrics[s.Phase+"_end"], float64(s.Finished)/float64(time.Millisecond))
					}
				}
				if err := e.Close(); err != nil {
					t.Fatal(err)
				}
				if fast.ScanCalled != 1 || slow.ScanCalled != 1 {
					t.Fatalf("scans=%d/%d", fast.ScanCalled, slow.ScanCalled)
				}
			}
			logStartupMetrics(t, metrics)
			t.Log("18 generated provider scans, 2 final rows/trial; logical source ScanWork=0 (fake providers)")
		})
	}
}

type startupScanRelease struct {
	provider.Provider
	entered, release chan struct{}
	once             sync.Once
}

func (p *startupScanRelease) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	first := false
	p.once.Do(func() { first = true; close(p.entered) })
	if first {
		select {
		case <-p.release:
		case <-ctx.Done():
			return provider.ScanResult{}, ctx.Err()
		}
	}
	return p.Provider.Scan(ctx, prev)
}

// A real catalog bus event must precede the held provider's release. No timing
// threshold or sleep decides ordering; the timeout only detects deadlock.
func TestStartupRunnerPublishesFastProvider(t *testing.T) {
	for _, mode := range []string{"disabled", "fallback"} {
		for _, cancelSlow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%t", mode, cancelSlow), func(t *testing.T) {
				root := t.TempDir()
				cache := filepath.Join(root, "unusable")
				if err := os.WriteFile(cache, []byte("generated"), 0o600); err != nil {
					t.Fatal(err)
				}
				fast := providertest.NewFake(model.AgentClaude, "generated-fast")
				slowFake := providertest.NewFake(model.AgentCodex, "generated-slow")
				for _, p := range []*providertest.Fake{fast, slowFake} {
					p.DetectionData.Present = true
					p.Sessions = []model.SessionMeta{{Ref: model.SessionRef{Agent: p.ID(), ID: "generated"}}}
				}
				slow := &startupScanRelease{Provider: slowFake, entered: make(chan struct{}), release: make(chan struct{})}
				emitter, statuses := discoveryEvents()
				catalogEvents := make(chan CatalogChanged, 10)
				e := NewEngine(WithCacheEnabled(mode != "disabled"), WithCacheDir(cache), WithService(NewService(scan.NewCatalog(), provider.Set{slow, fast})), WithEmitter(EmitterFunc(func(name string, payload any) {
					emitter.Emit(name, payload)
					if name == CatalogChangedEvent {
						catalogEvents <- payload.(CatalogChanged)
					}
				})))
				defer func() { _ = e.Close() }()
				if err := e.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				<-slow.entered
				select {
				case ev := <-catalogEvents:
					if !ev.GroupsDirty {
						t.Fatalf("event=%+v", ev)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("fast event waited for slow provider")
				}
				rows, err := e.ListSessions(t.Context(), "", FilterOpts{}, SortOpts{})
				if err != nil || len(rows) != 1 || rows[0].Ref.Agent != fast.ID() {
					t.Fatalf("fast rows=%v err=%v", rows, err)
				}
				s, _ := e.DiscoveryStatus(t.Context())
				if s.Complete || s.State != DiscoveryReconciling {
					t.Fatalf("premature completion=%+v", s)
				}
				if cancelSlow {
					if err := e.Close(); err != nil {
						t.Fatal(err)
					}
					// Old bus is stopped and its owned bootstrap joined before restart.
					for len(catalogEvents) > 0 {
						<-catalogEvents
					}
					for len(statuses) > 0 {
						<-statuses
					}
					close(slow.release)
					if err := e.Start(t.Context()); err != nil {
						t.Fatal(err)
					}
				} else {
					close(slow.release)
				}
				s = awaitDiscovery(t, statuses, func(s DiscoveryStatus) bool { return s.Complete })
				if s.State != DiscoverySettled || (cancelSlow && s.Lifetime != 2) {
					t.Fatalf("terminal=%+v", s)
				}
				rows, err = e.ListSessions(t.Context(), "", FilterOpts{}, SortOpts{})
				if err != nil || len(rows) != 2 {
					t.Fatalf("final rows=%v err=%v", rows, err)
				}
				select {
				case <-catalogEvents:
				case <-time.After(10 * time.Second):
					t.Fatal("final/restarted bus missing event")
				}
				if err := e.Close(); err != nil {
					t.Fatal(err)
				}
				wantFast := 1
				if cancelSlow {
					wantFast = 2
				}
				if fast.ScanCalled != wantFast || slowFake.ScanCalled != 1 {
					t.Fatalf("scans=%d/%d", fast.ScanCalled, slowFake.ScanCalled)
				}
			})
		}
	}
}
