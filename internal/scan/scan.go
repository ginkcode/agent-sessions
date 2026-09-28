// Package scan coordinates providers and keeps their session metadata in memory.
package scan

import (
	"context"
	"fmt"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

const defaultWorkers = 3

// Runner scans providers concurrently and applies results to a catalog.
// Do not call Run concurrently on the same Runner.
type Runner struct {
	Providers provider.Set
	Workers   int
	Catalog   *Catalog
}

// Report aggregates the outcome of a run over all providers.
type Report struct {
	States map[model.AgentID]provider.ScanState
	Diag   map[model.AgentID]provider.Diagnostics
	Errors map[model.AgentID]error
}

type outcome struct {
	id      model.AgentID
	result  provider.ScanResult
	live    map[string]provider.LiveInfo
	liveErr error
	hasLive bool
	err     error
}

// Run scans each detected provider. Failed and absent providers retain their
// previous state and catalog entries until a successful scan replaces them.
func (r *Runner) Run(ctx context.Context, prev map[model.AgentID]provider.ScanState) Report {
	report := Report{
		States: make(map[model.AgentID]provider.ScanState, len(prev)),
		Diag:   make(map[model.AgentID]provider.Diagnostics),
		Errors: make(map[model.AgentID]error),
	}
	for id, state := range prev {
		report.States[id] = state
	}
	if r.Catalog == nil {
		r.Catalog = NewCatalog()
	}

	// Duplicate IDs cannot share one state key. Refuse the run before applying
	// any results rather than racing two scans or choosing a winner by timing.
	seen := make(map[model.AgentID]bool, len(r.Providers))
	for _, p := range r.Providers {
		id := p.ID()
		if seen[id] {
			report.Errors[id] = fmt.Errorf("duplicate provider %q", id)
		}
		seen[id] = true
	}
	if len(report.Errors) != 0 {
		return report
	}

	// Detection is cheap compared with scanning; a failure is isolated to its
	// provider. In particular, absence is not an authoritative deletion.
	detected := make([]provider.Provider, 0, len(r.Providers))
	for _, p := range r.Providers {
		if err := ctx.Err(); err != nil {
			report.Errors[p.ID()] = err
			continue
		}
		d, err := p.Detect(ctx)
		if err != nil {
			report.Errors[p.ID()] = fmt.Errorf("detect: %w", err)
		} else if d.Present {
			detected = append(detected, p)
		}
	}

	workers := r.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	workers = min(workers, len(detected))
	work := make(chan provider.Provider)
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				o := outcome{id: p.ID()}
				o.result, o.err = p.Scan(ctx, prev[o.id])
				if o.err == nil && ctx.Err() == nil {
					if detector, ok := p.(provider.LiveDetector); ok {
						o.hasLive = true
						o.live, o.liveErr = detector.Live(ctx)
					}
				}
				if o.err == nil && ctx.Err() != nil {
					o.err = ctx.Err()
				}
				results <- o
			}
		}()
	}

	// Send work without waiting for a slow provider. Collect results in this
	// goroutine alone; providers and their workers never touch report/catalog.
	go func() {
		defer close(work)
		for _, p := range detected {
			select {
			case <-ctx.Done():
				return
			case work <- p:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	handled := make(map[model.AgentID]bool, len(detected))
	for o := range results {
		handled[o.id] = true
		if o.err != nil {
			report.Errors[o.id] = fmt.Errorf("scan: %w", o.err)
			continue
		}
		if r.Catalog != nil {
			r.Catalog.applyLive(o.id, o.result, o.live, o.liveErr, o.hasLive)
		}
		report.States[o.id] = o.result.State
		report.Diag[o.id] = o.result.Diag
		if o.liveErr != nil {
			report.Errors[o.id] = fmt.Errorf("live: %w", o.liveErr)
			d := report.Diag[o.id]
			d.Warn("", 0, "live: %v", o.liveErr)
			report.Diag[o.id] = d
		}
	}
	if err := ctx.Err(); err != nil {
		for _, p := range detected {
			if !handled[p.ID()] {
				report.Errors[p.ID()] = err
			}
		}
	}
	return report
}
