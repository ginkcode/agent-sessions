package scan_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestRunnerOnAppliedOutcomes(t *testing.T) {
	for _, outcome := range []string{"rows", "empty", "removal", "live_error", "scan_error", "detect_error", "absent", "duplicate", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			p := providertest.NewFake(model.AgentClaude, "generated")
			p.DetectionData.Present = true
			ref := model.SessionRef{Agent: p.ID(), ID: "generated"}
			p.Sessions = []model.SessionMeta{{Ref: ref}}
			cat := scan.NewCatalog()
			provs := provider.Set{p}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch outcome {
			case "empty":
				p.Sessions = nil
			case "removal":
				cat.Apply(provider.ScanResult{Changed: p.Sessions})
				p.Sessions = nil
				p.RemovedRefs = []model.SessionRef{ref}
			case "live_error":
				p.LiveErr = errors.New("generated")
			case "scan_error":
				p.ScanErr = errors.New("generated")
			case "detect_error":
				p.DetectErr = errors.New("generated")
			case "absent":
				p.DetectionData.Present = false
			case "duplicate":
				provs = append(provs, p)
			case "cancelled":
				cancel()
			}
			wantCalls := 0
			wantRows := 0
			switch outcome {
			case "rows", "live_error":
				wantCalls = 1
				wantRows = 1
			case "empty", "removal":
				wantCalls = 1
			}
			calls := 0
			r := scan.Runner{Providers: provs, Catalog: cat}
			report := r.RunWithOnApplied(ctx, nil, func(id model.AgentID) {
				calls++
				if id != p.ID() || len(cat.All()) != wantRows {
					t.Fatalf("callback precedes catalog application: %s rows=%d", id, len(cat.All()))
				}
			})
			if calls != wantCalls || len(cat.All()) != wantRows {
				t.Fatalf("calls=%d rows=%d", calls, len(cat.All()))
			}
			if wantCalls == 1 {
				if _, ok := report.States[p.ID()]; !ok {
					t.Fatal("missing final state")
				}
				if p.ScanCalled != 1 {
					t.Fatalf("scan count=%d", p.ScanCalled)
				}
			}
			if outcome == "live_error" && len(report.Errors) != 1 {
				t.Fatal("lost live error")
			}
		})
	}
}

type heldScan struct {
	*providertest.Fake
	entered, release chan struct{}
}

func (p *heldScan) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	close(p.entered)
	select {
	case <-p.release:
	case <-ctx.Done():
		return provider.ScanResult{}, ctx.Err()
	}
	return p.Fake.Scan(ctx, prev)
}

// The fast callback is observed while the slow scan is deterministically held.
func TestRunnerOnAppliedDoesNotWaitForSlowProvider(t *testing.T) {
	for _, release := range []bool{true, false} {
		fast := providertest.NewFake(model.AgentClaude, "generated-fast")
		slowFake := providertest.NewFake(model.AgentCodex, "generated-slow")
		for _, p := range []*providertest.Fake{fast, slowFake} {
			p.DetectionData.Present = true
			p.Sessions = []model.SessionMeta{{Ref: model.SessionRef{Agent: p.ID(), ID: "generated"}}}
		}
		slow := &heldScan{Fake: slowFake, entered: make(chan struct{}), release: make(chan struct{})}
		cat := scan.NewCatalog()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		applied := make(chan model.AgentID, 2)
		done := make(chan scan.Report, 1)
		r := scan.Runner{Providers: provider.Set{slow, fast}, Catalog: cat}
		go func() { done <- r.RunWithOnApplied(ctx, nil, func(id model.AgentID) { applied <- id }) }()
		<-slow.entered
		if id := <-applied; id != fast.ID() || len(cat.All()) != 1 || cat.All()[0].Ref.Agent != fast.ID() {
			t.Fatalf("fast callback id=%s rows=%v", id, cat.All())
		}
		if release {
			close(slow.release)
		} else {
			cancel()
		}
		report := <-done
		cancel()
		if release {
			if id := <-applied; id != slowFake.ID() || len(cat.All()) != 2 || len(report.Errors) != 0 || len(report.States) != 2 {
				t.Fatalf("final id=%s rows=%d report=%+v", id, len(cat.All()), report)
			}
		} else if len(applied) != 0 || len(cat.All()) != 1 || !errors.Is(report.Errors[slowFake.ID()], context.Canceled) {
			t.Fatalf("cancelled slow applied=%d rows=%d errs=%v", len(applied), len(cat.All()), report.Errors)
		}
		if fast.ScanCalled != 1 || slowFake.ScanCalled != map[bool]int{true: 1, false: 0}[release] {
			t.Fatalf("scans fast=%d slow=%d", fast.ScanCalled, slowFake.ScanCalled)
		}
	}
}
