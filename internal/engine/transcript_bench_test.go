package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func checkTranscript(tb testing.TB, tr *model.Transcript) {
	tb.Helper()
	if len(tr.Messages) != 2*providertest.TranscriptTurns {
		tb.Fatalf("messages=%d", len(tr.Messages))
	}
	tail := tr.Messages[len(tr.Messages)-1]
	if len(tail.Parts) != 4 || tail.Parts[3].Tool == nil || tail.Parts[3].Tool.ID != "tail" || tail.Parts[3].Tool.Status != model.ToolUnknown {
		tb.Fatal("valid unterminated split-tool tail missing")
	}
	for i := range providertest.TranscriptTurns {
		u, a := tr.Messages[2*i], tr.Messages[2*i+1]
		if u.Role != model.RoleUser || len(u.Parts) != 1 || a.Role != model.RoleAssistant || len(a.Parts) < 3 || a.Parts[0].Text != "first" || a.Parts[1].Text != "second" {
			tb.Fatalf("turn %d invalid", i)
		}
		tool := a.Parts[2].Tool
		if tool == nil || tool.Status != model.ToolCompleted || tool.Output != "done" {
			tb.Fatalf("turn %d tool=%+v", i, tool)
		}
	}
}

func TestTranscriptBaselineEquivalence(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			p, ref, _ := providertest.TranscriptFixture(t, agent, 0)
			small, err := p.Load(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			checkTranscript(t, small)
			for _, mb := range []int{38, 100} {
				t.Run(fmt.Sprint(mb), func(t *testing.T) {
					p, ref, size := providertest.TranscriptFixture(t, agent, mb*1000000)
					work := new(provider.LoadWork)
					scanWork := new(provider.ScanWork)
					ctx := provider.WithScanWork(provider.WithLoadWork(t.Context(), work), scanWork)
					tr, err := p.Load(ctx, ref)
					if err != nil {
						t.Fatal(err)
					}
					checkTranscript(t, tr)
					if work.Calls.Load() != 1 || work.SourceOpens.Load() != 1 || work.SourceBytes.Load() != size {
						t.Fatalf("load work calls=%d opens=%d bytes=%d size=%d", work.Calls.Load(), work.SourceOpens.Load(), work.SourceBytes.Load(), size)
					}
					// Normalize generated prompt length. Everything else in the output
					// messages (IDs, parts, tokens, tool pairing, ordering) must match.
					wantPrompt := strings.Repeat("x", mb*1000000/providertest.TranscriptTurns)
					for i := range providertest.TranscriptTurns {
						if tr.Messages[2*i].Parts[0].Text != wantPrompt {
							t.Fatalf("turn %d large text mismatch", i)
						}
						tr.Messages[2*i].Parts[0].Text = "x"
					}
					if tr.Meta.Ref != small.Meta.Ref || tr.Meta.Counts != small.Meta.Counts || tr.Meta.Tokens != small.Meta.Tokens || tr.Meta.Model != small.Meta.Model || !tr.Meta.CreatedAt.Equal(small.Meta.CreatedAt) {
						t.Fatalf("large metadata differs: %+v vs %+v", tr.Meta, small.Meta)
					}
					if !reflect.DeepEqual(tr.Messages, small.Messages) {
						t.Fatal("large messages differ from small equivalent")
					}
					svc := NewService(nil, provider.Set{p})
					first, err := svc.GetMessages(ctx, ref, -1, 50)
					if err != nil {
						t.Fatal(err)
					}
					warm, err := svc.GetMessages(ctx, ref, -1, 50)
					if err != nil || !reflect.DeepEqual(first, warm) {
						t.Fatal("warm page mismatch", err)
					}
					if work.Calls.Load() != 2 {
						t.Fatal("warm page called provider")
					}
					svc.EvictTranscript(ref.Key())
					reload, err := svc.GetMessages(ctx, ref, -1, 50)
					if err != nil || !reflect.DeepEqual(first, reload) || work.Calls.Load() != 3 {
						t.Fatal("invalidated page mismatch", err)
					}
				})
			}
		})
	}
}

func TestTranscriptConcurrentMissBaseline(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			fake := providertest.NewFake(model.AgentClaude, "synthetic")
			gate := providertest.NewBarrier(fake, 4)
			t.Cleanup(gate.Close)
			svc := NewService(nil, provider.Set{gate})
			results := make(chan MessagesPage, 4)
			errs := make(chan error, 4)
			refs := make([]model.SessionRef, 4)
			for i := range refs {
				id := fmt.Sprint(i)
				if same {
					id = "same"
				}
				refs[i] = model.SessionRef{Agent: model.AgentClaude, ID: id}
				go func(ref model.SessionRef) {
					page, err := svc.GetMessages(t.Context(), ref, -1, 50)
					results <- page
					errs <- err
				}(refs[i])
			}
			attempts := make([]*providertest.LoadAttempt, 4)
			for i := range attempts {
				attempts[i] = <-gate.Started
			}
			tr := &model.Transcript{Messages: []model.Message{{ID: "synthetic", Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "same snapshot"}}}}}
			// Release in reverse arrival order to make ordering explicit.
			for i := len(attempts) - 1; i >= 0; i-- {
				attempts[i].Reply(tr, nil)
			}
			for range refs {
				page := <-results
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
				if page.TotalCount != 1 || !reflect.DeepEqual(page.Messages, tr.Messages) {
					t.Fatal("concurrent output differs")
				}
			}
			if gate.Calls.Load() != 4 {
				t.Fatal("baseline should perform four loads")
			}
			for _, ref := range refs {
				if _, err := svc.GetMessages(t.Context(), ref, -1, 50); err != nil {
					t.Fatal(err)
				}
			}
			if gate.Calls.Load() != 4 {
				t.Fatal("warm requests called provider")
			}
		})
	}
}

// measuredLiveHeap is an actual runtime.MemStats HeapAlloc delta across one
// load, after forced GCs, with the transcript still reachable. It is process-wide
// and noisy (other live objects can change), unlike TranscriptEstimate.
func measuredLiveHeap(tb testing.TB, p provider.Provider, ref model.SessionRef) (int64, *model.Transcript) {
	tb.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	tr, err := p.Load(context.Background(), ref)
	if err != nil {
		tb.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(tr)
	return int64(after.HeapAlloc) - int64(before.HeapAlloc), tr
}

// sameKeyBaselineLoads is the current uncoalesced behavior: all four concurrent
// misses reach the provider. PF-05 is expected to change this to 1.
const sameKeyBaselineLoads = 4

// concurrentPages starts four requests and releases provider loads only after
// wantLoads calls are blocked, so every intended miss overlaps without sleeps.
func concurrentPages(svc *Service, gate *providertest.Barrier, ctx context.Context, refs []model.SessionRef, wantLoads int) error {
	errs := make(chan error, len(refs))
	for _, ref := range refs {
		go func() {
			_, err := svc.GetMessages(ctx, ref, -1, 50)
			errs <- err
		}()
	}
	attempts := make([]*providertest.LoadAttempt, 0, wantLoads)
	for range wantLoads {
		attempts = append(attempts, <-gate.Started)
	}
	for _, a := range attempts {
		a.Forward()
	}
	var err error
	for range refs {
		if e := <-errs; e != nil {
			err = e
		}
	}
	return err
}

func BenchmarkTranscript(b *testing.B) {
	for _, agent := range []string{"claude", "codex"} {
		for _, mb := range []int{38, 100} {
			b.Run(fmt.Sprintf("%s/%dMB", agent, mb), func(b *testing.B) {
				p, ref, size := providertest.TranscriptFixture(b, agent, mb*1000000)
				heap, tr := measuredLiveHeap(b, p, ref)
				checkTranscript(b, tr)
				estimate := providertest.TranscriptEstimate(tr)
				full, err := json.Marshal(tr)
				if err != nil {
					b.Fatal(err)
				}
				tr = nil
				for _, mode := range []string{"provider-cold", "cold-page", "warm-page", "invalidated", "same-key-4", "independent-key-4"} {
					b.Run(mode, func(b *testing.B) {
						ps := []provider.Provider{p}
						refs := []model.SessionRef{ref}
						if mode == "independent-key-4" {
							for i := 1; i < 4; i++ {
								q, r, _ := providertest.TranscriptFixture(b, agent, mb*1000000)
								ps = append(ps, q)
								refs = append(refs, model.SessionRef{Agent: r.Agent, ID: fmt.Sprintf("key-%d", i)})
							}
						}
						routed := &transcriptRoutes{Provider: p, providers: ps, refs: refs, original: ref}
						svc := NewService(nil, provider.Set{routed})
						page, err := svc.GetMessages(context.Background(), ref, -1, 50)
						if err != nil {
							b.Fatal(err)
						}
						payload, err := json.Marshal(page)
						if err != nil {
							b.Fatal(err)
						}
						work := new(provider.LoadWork)
						scans := new(provider.ScanWork)
						ctx := provider.WithScanWork(provider.WithLoadWork(context.Background(), work), scans)
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							switch mode {
							case "provider-cold":
								_, err = p.Load(ctx, ref)
							case "cold-page":
								b.StopTimer()
								svc = NewService(nil, provider.Set{routed})
								b.StartTimer()
								_, err = svc.GetMessages(ctx, ref, -1, 50)
							case "warm-page":
								_, err = svc.GetMessages(ctx, ref, -1, 50)
							case "invalidated":
								svc.EvictTranscript(ref.Key())
								_, err = svc.GetMessages(ctx, ref, -1, 50)
							default:
								b.StopTimer()
								gate := providertest.NewBarrier(routed, 4)
								svc = NewService(nil, provider.Set{gate})
								batch := refs
								if len(refs) == 1 {
									batch = []model.SessionRef{ref, ref, ref, ref}
								}
								b.StartTimer()
								err = concurrentPages(svc, gate, ctx, batch, sameKeyBaselineLoads)
								b.StopTimer()
								gate.Close()
								b.StartTimer()
							}
							if err != nil {
								b.Fatal(err)
							}
						}
						b.StopTimer()
						want := int64(b.N)
						if mode == "warm-page" {
							want = 0
						}
						if mode == "same-key-4" || mode == "independent-key-4" {
							want *= 4
						}
						if work.Calls.Load() != want || work.SourceOpens.Load() != want || work.SourceBytes.Load() != want*size {
							b.Fatalf("loads=%d want=%d bytes=%d want=%d", work.Calls.Load(), want, work.SourceBytes.Load(), want*size)
						}
						b.ReportMetric(float64(work.Calls.Load())/float64(b.N), "loads/op")
						b.ReportMetric(float64(work.SourceOpens.Load())/float64(b.N), "source-opens/op")
						b.ReportMetric(float64(work.SourceBytes.Load())/float64(b.N), "source-B/op")
						b.ReportMetric(float64(scans.RecordBytes.Load())/float64(b.N), "resolve-record-B/op")
						b.ReportMetric(float64(len(payload)), "page-JSON-B")
						b.ReportMetric(float64(len(full)), "transcript-JSON-B")
						b.ReportMetric(float64(estimate), "retained-est-B/transcript")
						b.ReportMetric(float64(heap), "heap-live-B/transcript")
					})
				}
			})
		}
	}
}

type transcriptRoutes struct {
	provider.Provider
	providers []provider.Provider
	refs      []model.SessionRef
	original  model.SessionRef
}

func (p *transcriptRoutes) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	for i, r := range p.refs {
		if r == ref {
			return p.providers[i].Load(ctx, p.original)
		}
	}
	return nil, fmt.Errorf("unknown generated ref %s", ref.Key())
}
