package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
	tr := &model.Transcript{}
	for i := range 1000 {
		key := fmt.Sprint(i)
		_, load := c.begin(key)
		c.finish(key, load, tr, nil)
		// No historical tokens remain after completion or absent-key eviction.
		c.evict("absent-" + key)
		if len(c.active) != 0 || c.lru.Len() > transcriptLRUCapacity {
			t.Fatalf("unbounded state at %d: active=%d cached=%d", i, len(c.active), c.lru.Len())
		}
	}
	if c.lru.Len() != transcriptLRUCapacity {
		t.Fatal("cache capacity changed")
	}
	// LRU recency and automatic capacity eviction do not invalidate pending work.
	_, pending := c.begin("pending")
	// Fill beyond capacity while that independent provider load is blocked.
	for i := range 10 {
		key := fmt.Sprintf("filler-%d", i)
		_, load := c.begin(key)
		c.finish(key, load, tr, nil)
	}
	if len(c.active) != 1 {
		t.Fatal("capacity eviction lost pending identity")
	}
	// Restore a known recency order for the LRU regression below.
	for i := 996; i < 1000; i++ {
		key := fmt.Sprint(i)
		_, load := c.begin(key)
		c.finish(key, load, tr, nil)
	}
	if _, hit := c.lru.Get("996"); !hit {
		t.Fatal("expected resident entry")
	}
	c.finish("pending", pending, tr, nil)
	if got, hit := c.lru.Get("pending"); !hit || got != tr {
		t.Fatal("capacity eviction rejected useful pending load")
	}
	if _, hit := c.lru.Get("997"); hit {
		t.Fatal("LRU did not evict oldest entry")
	}
	if _, hit := c.lru.Get("996"); !hit {
		t.Fatal("LRU lost recently used entry")
	}
	// Many concurrent misses share identity only, not I/O. Finishing one must
	// not retire the token while another can still publish.
	_, first := c.begin("same")
	_, second := c.begin("same")
	c.finish("same", first, nil, errors.New("synthetic failure"))
	if len(c.active) != 1 {
		t.Fatal("pending identity retired too early")
	}
	c.finish("same", second, tr, nil)
	if len(c.active) != 0 {
		t.Fatal("identity retained after final completion")
	}
	// Repeated invalidation of outstanding work retains no tombstones. Stale
	// completion must not retire a newer token, even after repeated clear/evict.
	c.evict("same")
	for i := range 1000 {
		_, old := c.begin("same")
		c.evict("same")
		_, fresh := c.begin("same")
		c.finish("same", old, tr, nil)
		if len(c.active) != 1 {
			t.Fatal("old completion removed current identity")
		}
		c.clear()
		c.finish("same", fresh, tr, nil)
		if len(c.active) != 0 || c.lru.Len() != 0 {
			t.Fatalf("clear retained state at %d", i)
		}
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
		if len(svc.transcripts.active) != 0 || svc.transcripts.lru.Len() != 0 {
			t.Fatal("failed load retained state")
		}
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
	if len(svc.transcripts.active) != 0 || svc.transcripts.lru.Len() != 0 {
		t.Fatal("cancelled load retained state")
	}
	a, retry := startTranscript(t, svc, gate, ref)
	tr := &model.Transcript{}
	a.Reply(tr, nil)
	if got := <-retry; got != tr {
		t.Fatal("retry after cancellation failed")
	}
}
