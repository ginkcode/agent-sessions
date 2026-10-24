package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func TestStartupObservation(t *testing.T) {
	o := NewStartupObservation()
	o.begin("groups")(false)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { o.begin("groups")(true) })
	}
	wg.Wait()
	got := o.Samples()
	if len(got) != 1 || got[0].Phase != "groups" || !got[0].Success || got[0].Finished < got[0].Started {
		t.Fatalf("bad samples: %+v", got)
	}
	got[0].Phase = "modified"
	if o.Samples()[0].Phase != "groups" {
		t.Fatal("snapshot aliases collector")
	}
	startupObservation(context.Background()).begin("disabled")(true)
}

// TestStartupBaseline is opt-in: fixtures, caches and roots are generated here.
// It times full Engine.Start plus its asynchronous reconciliation, not fixture
// construction, cache seeding or Close. No native app or private roots are used.
func TestStartupBaseline(t *testing.T) {
	if os.Getenv("AS_STARTUP_BASELINE") != "1" {
		t.Skip("set AS_STARTUP_BASELINE=1 for repeated generated full startup")
	}
	root, roots := startupGeneratedFixture(t)
	// Seed through a full engine startup, excluded from all measured intervals.
	seed := NewEngine(WithRoots(roots))
	runStartupTrial(t, seed, false)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cold_writer", "warm_writer", "warm_reader", "disabled", "fallback"} {
		t.Run(mode, func(t *testing.T) {
			metrics := map[string][]float64{}
			var sums [5]int64
			for rep := range 9 {
				trialRoots := roots
				opts := []Option{}
				switch mode {
				case "cold_writer":
					trialRoots.Cache = filepath.Join(root, fmt.Sprintf("cold-%d", rep))
				case "disabled":
					opts = append(opts, WithCacheEnabled(false))
				case "fallback":
					trialRoots.Cache = filepath.Join(root, fmt.Sprintf("unusable-%d", rep))
					if err := os.WriteFile(trialRoots.Cache, []byte("not a directory"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				var lock *index.Lock
				if mode == "warm_reader" {
					var err error
					lock, err = index.TryLock(roots.Cache, index.RootsKey(roots.Claude, roots.Codex, roots.OpenCodeData))
					if err != nil {
						t.Fatal(err)
					}
				}
				e := NewEngine(append(opts, WithRoots(trialRoots))...)
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
				if lock != nil {
					_ = lock.Unlock()
				}
			}
			logStartupMetrics(t, metrics)
			t.Logf("aggregate scan work opens/records/record_bytes/checkpoint_decodes/checkpoint_encodes=%v", sums)
		})
	}
}

func startupGeneratedFixture(t *testing.T) (string, paths.Roots) {
	t.Helper()
	root := t.TempDir()
	roots := paths.Roots{Claude: filepath.Join(root, "claude"), Codex: filepath.Join(root, "codex"), OpenCodeData: filepath.Join(root, "opencode"), Cache: filepath.Join(root, "cache")}
	project := filepath.Join(roots.Claude, "projects", "generated")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	// 256 sessions, 16 records each. CWD is generated and has no git ancestors
	// below the temporary root; prompts are fixed synthetic strings.
	var fixtureBytes int
	for i := range 256 {
		var body string
		for j := range 16 {
			body += fmt.Sprintf("{\"type\":\"user\",\"uuid\":\"%d-%d\",\"sessionId\":\"generated-%d\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"cwd\":%q,\"message\":{\"role\":\"user\",\"content\":\"generated startup fixture\"}}\n", i, j, i, root)
		}
		fixtureBytes += len(body)
		if err := os.WriteFile(filepath.Join(project, fmt.Sprintf("generated-%d.jsonl", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("fixture sessions=256 records=4096 bytes=%d; repeats=9", fixtureBytes)
	return root, roots
}

func logStartupMetrics(t *testing.T, metrics map[string][]float64) {
	t.Helper()
	keys := make([]string, 0, len(metrics))
	for k := range metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := metrics[k]
		sort.Float64s(v)
		t.Logf("%s ms min/median/p95/max=%.3f/%.3f/%.3f/%.3f n=%d", k, v[0], v[len(v)/2], v[(95*len(v)+99)/100-1], v[len(v)-1], len(v))
	}
}

func runStartupTrial(t *testing.T, e *Engine, observed bool) ([]StartupSample, *provider.ScanWork) {
	t.Helper()
	o := NewStartupObservation()
	work := &provider.ScanWork{}
	ctx := provider.WithScanWork(WithStartupObservation(context.Background(), o), work)
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Query immediately after Start, matching the synchronous app startup boundary.
	if _, err := e.ListGroups(ctx, GroupMode("dir-agent"), FilterOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AgentCounts(ctx, FilterOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ListSessions(ctx, "", FilterOpts{}, SortOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		samples := o.Samples()
		for _, s := range samples {
			if s.Phase == "bootstrap" {
				if !s.Success {
					t.Fatal("bootstrap failed")
				}
				rows, err := e.ListSessions(ctx, "", FilterOpts{}, SortOpts{})
				if err != nil || len(rows) != 256 {
					t.Fatalf("final catalog: rows=%d err=%v", len(rows), err)
				}
				if observed {
					assertStartupOrder(t, samples)
				}
				return samples, work
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("bootstrap did not finish")
		case <-tick.C:
		}
	}
}

func assertStartupOrder(t *testing.T, samples []StartupSample) {
	t.Helper()
	by := map[string]StartupSample{}
	for _, s := range samples {
		by[s.Phase] = s
		if s.Finished < s.Started {
			t.Fatal("negative interval")
		}
	}
	if pub, ok := by["catalog_publication"]; ok {
		if pub.Finished > by["bootstrap"].Started {
			t.Fatal("bootstrap precedes cached publication")
		}
		if pub.Finished > by["watcher_setup"].Started {
			t.Fatal("watcher precedes cached publication")
		}
	}
	for _, phase := range []string{"start", "bootstrap", "groups", "counts", "sessions"} {
		if _, ok := by[phase]; !ok {
			t.Fatalf("missing phase %s", phase)
		}
	}
}

// Hold synchronous watcher registration, not a machine-dependent sleep. Cached
// catalog queries must remain outside e.mu and precede bootstrap in both roles.
type startupWatchBarrier struct {
	provider.Provider
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *startupWatchBarrier) WatchPaths() []string {
	p.once.Do(func() { close(p.entered); <-p.release })
	return nil
}

func TestStartupPublicationQueryableDuringWatcherSetup(t *testing.T) {
	for _, reader := range []bool{false, true} {
		t.Run(fmt.Sprint(reader), func(t *testing.T) {
			dir := t.TempDir()
			roots := paths.Roots{Claude: filepath.Join(dir, "generated"), Cache: filepath.Join(dir, "cache")}
			key := index.RootsKey(roots.Claude, "", "")
			db, err := index.Open(t.Context(), roots.Cache, key)
			if err != nil {
				t.Fatal(err)
			}
			meta := model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "generated"}}
			if err := db.CommitScan(t.Context(), model.AgentClaude, provider.ScanResult{Changed: []model.SessionMeta{meta}}); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			if reader {
				lock, err := index.TryLock(roots.Cache, key)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Unlock() }()
			}
			fake := providertest.NewFake(model.AgentClaude, "generated")
			fresh := model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "generated-fresh"}}
			fake.Sessions = []model.SessionMeta{meta, fresh}
			fake.DetectionData = provider.Detection{Present: true}
			barrier := &startupWatchBarrier{Provider: fake, entered: make(chan struct{}), release: make(chan struct{})}
			o := NewStartupObservation()
			ctx := WithStartupObservation(t.Context(), o)
			emitter, statuses := discoveryEvents()
			e := NewEngine(WithRoots(roots), WithService(NewService(scan.NewCatalog(), provider.Set{barrier})), WithEmitter(emitter))
			finished := make(chan error, 1)
			go func() { finished <- e.Start(ctx) }()
			<-barrier.entered
			// Release even on test failure; Close only after Start has returned.
			var releaseOnce sync.Once
			release := func() error {
				var err error
				releaseOnce.Do(func() { close(barrier.release); err = <-finished })
				return err
			}
			defer func() {
				if err := release(); err != nil {
					t.Error(err)
				}
				_ = e.Close()
			}()
			statusReply := make(chan DiscoveryStatus, 1)
			go func() { s, _ := e.DiscoveryStatus(ctx); statusReply <- s }()
			select {
			case s := <-statusReply:
				mode := DiscoveryCacheWriter
				if reader {
					mode = DiscoveryCacheReader
				}
				if s.State != DiscoveryReconciling || !s.CatalogUsable || s.Complete || s.DetectionComplete || s.CacheMode != mode {
					t.Fatalf("held-startup status=%+v", s)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("status read blocked on startup mutex")
			}
			rows, err := e.ListSessions(ctx, "", FilterOpts{}, SortOpts{})
			if err != nil || len(rows) != 1 {
				t.Fatalf("cached rows=%d err=%v", len(rows), err)
			}
			if _, err := e.ListGroups(ctx, GroupMode("dir-agent"), FilterOpts{}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.AgentCounts(ctx, FilterOpts{}); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, s := range o.Samples() {
				seen[s.Phase] = true
			}
			for _, phase := range []string{"catalog_publication", "sessions", "groups", "counts"} {
				if !seen[phase] {
					t.Fatalf("missing %s", phase)
				}
			}
			if seen["watcher_setup"] || seen["bootstrap"] || seen["start"] {
				t.Fatal("held phase completed early")
			}
			// ST-05: releasing the held phase reaches a settled terminal status
			// and the final catalog matches the providers, not the cached hint.
			if err := release(); err != nil {
				t.Fatal(err)
			}
			s := awaitDiscovery(t, statuses, func(s DiscoveryStatus) bool { return s.Complete })
			if s.State != DiscoverySettled || s.DetectedProviders != 1 || !s.CatalogUsable {
				t.Fatalf("terminal status=%+v", s)
			}
			rows, err = e.ListSessions(ctx, "", FilterOpts{}, SortOpts{})
			if err != nil || len(rows) != 2 {
				t.Fatalf("final rows=%d err=%v", len(rows), err)
			}
			if counts, err := e.AgentCounts(ctx, FilterOpts{}); err != nil || counts[string(model.AgentClaude)] != 2 {
				t.Fatalf("final counts=%v err=%v", counts, err)
			}
		})
	}
}
