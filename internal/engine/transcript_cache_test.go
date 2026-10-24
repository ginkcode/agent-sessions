package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

// The barrier sits inside provider I/O: reaching Started proves begin completed,
// and receiving the result proves finish completed. Neither requires sleeps.
func startTranscript(t *testing.T, svc *Service, gate *providertest.Barrier, ref model.SessionRef) (*providertest.LoadAttempt, <-chan *model.Transcript) {
	t.Helper()
	result := make(chan *model.Transcript, 1)
	go func() {
		tr, err := svc.loadTranscript(t.Context(), ref)
		if err != nil {
			t.Errorf("load: %v", err)
		}
		result <- tr
	}()
	return <-gate.Started, result
}

func TestTranscriptInvalidationPublication(t *testing.T) {
	for _, mode := range []string{"single", "batch", "clear"} {
		for _, oldFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oldFirst=%v", mode, oldFirst), func(t *testing.T) {
				gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 2)
				t.Cleanup(gate.Close)
				svc := NewService(nil, provider.Set{gate})
				ref := model.SessionRef{Agent: model.AgentClaude, ID: "reappearing"}
				old := &model.Transcript{Meta: model.SessionMeta{Ref: ref, Title: "old snapshot"}}
				fresh := &model.Transcript{Meta: model.SessionMeta{Ref: ref, Title: "new snapshot"}}
				a, oldResult := startTranscript(t, svc, gate, ref)
				switch mode {
				case "single":
					svc.EvictTranscript(ref.Key())
				case "batch":
					// Removed session, then reappearance with the same key.
					svc.EvictTranscripts(nil, []model.SessionRef{ref})
				case "clear":
					svc.ClearTranscripts()
				}
				b, newResult := startTranscript(t, svc, gate, ref)
				finishOld := func() {
					a.Reply(old, nil)
					if got := <-oldResult; got != old {
						t.Fatal("old caller lost its snapshot")
					}
				}
				if oldFirst {
					finishOld()
					if svc.transcripts.lru.Len() != 0 {
						t.Fatal("old completion published after invalidation")
					}
				}
				b.Reply(fresh, nil)
				if got := <-newResult; got != fresh {
					t.Fatal("new caller lost its snapshot")
				}
				if !oldFirst {
					finishOld()
				}
				got, err := svc.loadTranscript(t.Context(), ref)
				if err != nil || got != fresh || gate.Calls.Load() != 2 {
					t.Fatalf("current cache = %p, %v; calls=%d", got, err, gate.Calls.Load())
				}
				if len(svc.transcripts.active) != 0 {
					t.Fatal("completed loads retained bookkeeping")
				}
				// Publication before invalidation must be removed as well.
				svc.EvictTranscript(ref.Key())
				if svc.transcripts.lru.Len() != 0 {
					t.Fatal("published entry survived eviction")
				}
			})
		}
	}
}

func TestTranscriptUnrelatedInvalidation(t *testing.T) {
	gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 2)
	t.Cleanup(gate.Close)
	svc := NewService(nil, provider.Set{gate})
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "useful"}
	other := model.SessionRef{Agent: model.AgentClaude, ID: "other"}
	a, result := startTranscript(t, svc, gate, ref)
	// Invalidate both absent and resident unrelated keys, including empty batches.
	b, otherResult := startTranscript(t, svc, gate, other)
	b.Reply(&model.Transcript{}, nil)
	<-otherResult
	svc.EvictTranscript(other.Key())
	svc.EvictTranscripts([]model.SessionRef{other})
	svc.EvictTranscripts()
	svc.EvictTranscript("never-loaded")
	tr := &model.Transcript{}
	a.Reply(tr, nil)
	<-result
	got, err := svc.loadTranscript(t.Context(), ref)
	if err != nil || got != tr || gate.Calls.Load() != 2 {
		t.Fatalf("useful load rejected: %p, %v; calls=%d", got, err, gate.Calls.Load())
	}
}

func TestTranscriptCacheBookkeepingAndCapacity(t *testing.T) {
	c := newTranscriptCache(transcriptLRUCapacity)
	t.Cleanup(c.stop)
	tr := &model.Transcript{}
	for i := range 1000 {
		key := fmt.Sprint(i)
		got, err := c.load(t.Context(), key, func(context.Context) (*model.Transcript, error) { return tr, nil })
		if got != tr || err != nil {
			t.Fatal(err)
		}
		c.evict("absent-" + key)
		c.mu.Lock()
		if len(c.active) != 0 || len(c.owned) != 0 || c.lru.Len() > transcriptLRUCapacity {
			t.Fatal("retained bookkeeping")
		}
		c.mu.Unlock()
	}
	if c.lru.Len() != transcriptLRUCapacity {
		t.Fatal("capacity changed")
	}
	if _, hit := c.lru.Get("996"); !hit {
		t.Fatal("expected resident entry")
	}
	_, _ = c.load(t.Context(), "new", func(context.Context) (*model.Transcript, error) { return tr, nil })
	if _, hit := c.lru.Get("997"); hit {
		t.Fatal("LRU did not evict oldest entry")
	}
	if _, hit := c.lru.Get("996"); !hit {
		t.Fatal("LRU lost recently used entry")
	}
}

func TestTranscriptLoadFailureCleanup(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "synthetic")
	svc := NewService(nil, provider.Set{fake})
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "retry"}
	for range 10 {
		if _, err := svc.loadTranscript(t.Context(), ref); err == nil {
			t.Fatal("expected missing session error")
		}
		svc.transcripts.mu.Lock()
		if len(svc.transcripts.active) != 0 || svc.transcripts.lru.Len() != 0 {
			t.Fatal("failed load retained state")
		}
		svc.transcripts.mu.Unlock()
	}
	tr := &model.Transcript{}
	fake.Transcripts[ref.ID] = tr
	if got, err := svc.loadTranscript(t.Context(), ref); got != tr || err != nil {
		t.Fatalf("retry: %p, %v", got, err)
	}
	ref.Agent = "unknown"
	if _, err := svc.loadTranscript(t.Context(), ref); !errors.Is(err, ErrUnknownProvider) || len(svc.transcripts.active) != 0 {
		t.Fatalf("unknown provider cleanup: %v", err)
	}
}

func TestTranscriptCancelledLoadCleanup(t *testing.T) {
	gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 1)
	t.Cleanup(gate.Close)
	svc := NewService(nil, provider.Set{gate})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "cancel"}
	result := make(chan error, 1)
	go func() {
		_, err := svc.loadTranscript(ctx, ref)
		result <- err
	}()
	<-gate.Started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	svc.transcripts.mu.Lock()
	if len(svc.transcripts.active) != 0 || svc.transcripts.lru.Len() != 0 {
		t.Fatal("cancelled load retained state")
	}
	svc.transcripts.mu.Unlock()
	a, retry := startTranscript(t, svc, gate, ref)
	tr := &model.Transcript{}
	a.Reply(tr, nil)
	if got := <-retry; got != tr {
		t.Fatal("retry after cancellation failed")
	}
}

// Observe coordinator state rather than assuming goroutine scheduling order.
func waitTranscriptState(t *testing.T, c *transcriptCache, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		ok := ready()
		c.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("coordinator state not reached")
		default:
			runtime.Gosched()
		}
	}
}

func requestTranscript(ctx context.Context, svc *Service, ref model.SessionRef) <-chan error {
	result := make(chan error, 1)
	go func() { _, err := svc.loadTranscript(ctx, ref); result <- err }()
	return result
}

func TestTranscriptSharedCancellationAndRetry(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(cancelFirst), func(t *testing.T) {
			gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 4)
			t.Cleanup(gate.Close)
			svc := NewService(nil, provider.Set{gate})
			t.Cleanup(svc.transcripts.stop)
			ref := model.SessionRef{Agent: model.AgentClaude, ID: "shared"}
			first, cancel1 := context.WithCancel(t.Context())
			defer cancel1()
			second, cancel2 := context.WithCancel(t.Context())
			defer cancel2()
			r1 := requestTranscript(first, svc, ref)
			a := <-gate.Started
			r2 := requestTranscript(second, svc, ref)
			waitTranscriptState(t, svc.transcripts, func() bool { return svc.transcripts.active[ref.Key()].waiters == 2 })
			cancelled, remaining := r1, r2
			if cancelFirst {
				cancel1()
			} else {
				cancel2()
				cancelled, remaining = r2, r1
			}
			if err := <-cancelled; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if a.Context.Err() != nil {
				t.Fatal("departing waiter cancelled shared work")
			}
			failure := errors.New("shared failure")
			a.Reply(nil, failure)
			if err := <-remaining; !errors.Is(err, failure) {
				t.Fatal(err)
			}
			// A failure is not cached; the last departing waiter cancels retry work.
			ctx, cancel := context.WithCancel(t.Context())
			retry := requestTranscript(ctx, svc, ref)
			b := <-gate.Started
			cancel()
			if err := <-retry; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			<-b.Context.Done()
			waitTranscriptState(t, svc.transcripts, func() bool { return len(svc.transcripts.owned) == 0 })
			c, result := startTranscript(t, svc, gate, ref)
			tr := &model.Transcript{}
			c.Reply(tr, nil)
			if <-result != tr || gate.Calls.Load() != 3 {
				t.Fatal("retry did not reload")
			}
		})
	}
}

func TestTranscriptSharedSuccess(t *testing.T) {
	gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 4)
	t.Cleanup(gate.Close)
	svc := NewService(nil, provider.Set{gate})
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "shared"}
	results := []<-chan error{requestTranscript(t.Context(), svc, ref)}
	a := <-gate.Started
	for range 3 {
		results = append(results, requestTranscript(t.Context(), svc, ref))
	}
	waitTranscriptState(t, svc.transcripts, func() bool { return svc.transcripts.active[ref.Key()].waiters == 4 })
	a.Reply(&model.Transcript{}, nil)
	for _, result := range results {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	if gate.Calls.Load() != 1 {
		t.Fatal("same generation was not coalesced")
	}
}

func TestTranscriptBoundAndQueuedCancellation(t *testing.T) {
	gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 10)
	t.Cleanup(gate.Close)
	svc := NewService(nil, provider.Set{gate})
	t.Cleanup(svc.transcripts.stop)
	var results []<-chan error
	var attempts []*providertest.LoadAttempt
	for i := range transcriptLoadLimit {
		results = append(results, requestTranscript(t.Context(), svc, model.SessionRef{Agent: model.AgentClaude, ID: fmt.Sprint(i)}))
		attempts = append(attempts, <-gate.Started)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "queued"}
	queued := requestTranscript(ctx, svc, ref)
	waitTranscriptState(t, svc.transcripts, func() bool { return len(svc.transcripts.active) == transcriptLoadLimit+1 })
	if gate.Calls.Load() != transcriptLoadLimit {
		t.Fatal("provider limit exceeded")
	}
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitTranscriptState(t, svc.transcripts, func() bool { return len(svc.transcripts.owned) == transcriptLoadLimit })
	for _, a := range attempts {
		a.Reply(&model.Transcript{}, nil)
	}
	for _, r := range results {
		if err := <-r; err != nil {
			t.Fatal(err)
		}
	}
	if gate.Calls.Load() != transcriptLoadLimit {
		t.Fatal("cancelled queue reached provider")
	}
	// A later request can acquire a released slot (no leaked semaphore token).
	a, retry := startTranscript(t, svc, gate, ref)
	a.Reply(&model.Transcript{}, nil)
	<-retry
}

func TestTranscriptEngineLifetime(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(fmt.Sprint(start), func(t *testing.T) {
			gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 4)
			t.Cleanup(gate.Close)
			svc := NewService(nil, provider.Set{gate})
			e := NewEngine(WithService(svc), WithCacheEnabled(false))
			// No scan runner: isolate transcript lifetime, including cache-disabled Start.
			e.runner = nil
			t.Cleanup(func() { _ = e.Close() })
			if start {
				if err := e.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			ref := model.SessionRef{Agent: model.AgentClaude, ID: "owned"}
			result := requestTranscript(t.Context(), svc, ref)
			a := <-gate.Started
			svc.ClearTranscripts() // Detached flights are still owned and cancelled.
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			<-a.Context.Done()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := svc.loadTranscript(t.Context(), ref); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := e.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			b, retry := startTranscript(t, svc, gate, ref)
			tr := &model.Transcript{}
			b.Reply(tr, nil)
			if <-retry != tr {
				t.Fatal("restart retained cancelled lifetime")
			}
			if err := e.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := svc.loadTranscript(t.Context(), ref)
			if got != tr || err != nil {
				t.Fatal("idempotent Start cleared cache")
			}
		})
	}
}

func TestTranscriptOrphanCannotPublishOverRetry(t *testing.T) {
	gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 4)
	gate.IgnoreCancellation = true
	t.Cleanup(gate.Close)
	svc := NewService(nil, provider.Set{gate})
	t.Cleanup(svc.transcripts.stop)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "orphan"}
	ctx, cancel := context.WithCancel(t.Context())
	result := requestTranscript(ctx, svc, ref)
	old := <-gate.Started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fresh, retry := startTranscript(t, svc, gate, ref)
	old.Reply(&model.Transcript{}, nil)
	waitTranscriptState(t, svc.transcripts, func() bool { return len(svc.transcripts.owned) == 1 })
	// A third waiter must join the still-running retry, not the completed orphan.
	third := requestTranscript(t.Context(), svc, ref)
	waitTranscriptState(t, svc.transcripts, func() bool { return svc.transcripts.active[ref.Key()].waiters == 2 })
	tr := &model.Transcript{}
	fresh.Reply(tr, nil)
	if <-retry != tr {
		t.Fatal("old orphan displaced retry")
	}
	if err := <-third; err != nil {
		t.Fatal(err)
	}
	if gate.Calls.Load() != 2 {
		t.Fatal("old completion removed current flight")
	}
}

func TestTranscriptShutdownUncooperativeProvider(t *testing.T) {
	gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 4)
	gate.IgnoreCancellation = true
	t.Cleanup(gate.Close)
	svc := NewService(nil, provider.Set{gate})
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "blocked"}
	result := requestTranscript(t.Context(), svc, ref)
	old := <-gate.Started
	svc.transcripts.stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	svc.transcripts.start(t.Context())
	fresh, retry := startTranscript(t, svc, gate, ref)
	tr := &model.Transcript{}
	fresh.Reply(tr, nil)
	if <-retry != tr {
		t.Fatal("restart failed")
	}
	old.Reply(&model.Transcript{}, nil)
	waitTranscriptState(t, svc.transcripts, func() bool { return len(svc.transcripts.owned) == 0 })
	got, err := svc.loadTranscript(t.Context(), ref)
	if got != tr || err != nil {
		t.Fatal("shutdown load republished")
	}
}

func TestTranscriptLastSharedWaiterAndLifetimeCancellation(t *testing.T) {
	for _, lifetime := range []bool{false, true} {
		t.Run(fmt.Sprint(lifetime), func(t *testing.T) {
			gate := providertest.NewBarrier(providertest.NewFake(model.AgentClaude, "synthetic"), 4)
			t.Cleanup(gate.Close)
			svc := NewService(nil, provider.Set{gate})
			owner, stop := context.WithCancel(t.Context())
			defer stop()
			svc.transcripts.start(owner)
			ref := model.SessionRef{Agent: model.AgentClaude, ID: "shared"}
			ctx1, cancel1 := context.WithCancel(t.Context())
			defer cancel1()
			ctx2, cancel2 := context.WithCancel(t.Context())
			defer cancel2()
			first := requestTranscript(ctx1, svc, ref)
			a := <-gate.Started
			second := requestTranscript(ctx2, svc, ref)
			waitTranscriptState(t, svc.transcripts, func() bool { return svc.transcripts.active[ref.Key()].waiters == 2 })
			cancel1()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if lifetime {
				stop()
			} else {
				cancel2()
			}
			if err := <-second; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			<-a.Context.Done()
			waitTranscriptState(t, svc.transcripts, func() bool { return len(svc.transcripts.owned) == 0 && len(svc.transcripts.active) == 0 })
		})
	}
}
