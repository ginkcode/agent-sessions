package engine

import (
	"context"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// DiscoveryStatusEvent carries a DiscoveryStatus whenever it changes. It is
// separate from catalog:changed and index:progress: neither implies discovery.
const DiscoveryStatusEvent = "discovery:status"

// Discovery states. Settled, degraded and failed are terminal for an attempt.
const (
	DiscoveryInitializing = "initializing"
	DiscoveryRestoring    = "restoring"
	DiscoveryReconciling  = "reconciling"
	DiscoverySettled      = "settled"
	DiscoveryDegraded     = "degraded"
	DiscoveryFailed       = "failed"
)

// Discovery cache modes.
const (
	DiscoveryCacheNone     = "none" // zero configured providers; no cache opened
	DiscoveryCacheDisabled = "disabled"
	DiscoveryCacheWriter   = "writer"
	DiscoveryCacheReader   = "reader"
	DiscoveryCacheFallback = "fallback" // cache unusable; in-memory scan instead
)

// DiscoveryStatus describes startup provider discovery, not FTS indexing.
//
// Lifetime increases on every Start; Revision increases on every change within
// a lifetime, so (Lifetime, Revision) orders snapshots and events. A consumer
// ignores anything older than what it holds and refetches on a new lifetime.
//
// CatalogUsable means catalog queries are served, including a legitimately
// empty catalog; during reconciliation rows are cached or partial hints.
// Complete means the attempt reached settled, degraded or failed. Provider
// counts come only from Detect/Scan results of the latest discovery attempt,
// never from catalog rows. DetectionComplete reports that every configured
// provider answered Detect, so DetectedProviders == 0 with DetectionComplete
// and no failures is truthful "no agents detected". A cache failure followed by
// a successful in-memory scan settles with CacheMode "fallback"; it is not a
// failure. Isolated provider failures degrade, they are not fatal.
type DiscoveryStatus struct {
	Lifetime            uint64 `json:"lifetime"`
	Revision            uint64 `json:"revision"`
	State               string `json:"state"`
	CatalogUsable       bool   `json:"catalogUsable"`
	Complete            bool   `json:"complete"`
	CacheMode           string `json:"cacheMode,omitempty"`
	ConfiguredProviders int    `json:"configuredProviders"`
	DetectedProviders   int    `json:"detectedProviders"`
	FailedProviders     int    `json:"failedProviders"`
	DetectionComplete   bool   `json:"detectionComplete"`
}

// DiscoveryBackend is optional so older host backends remain compatible.
type DiscoveryBackend interface {
	DiscoveryStatus(context.Context) (DiscoveryStatus, error)
}

var _ DiscoveryBackend = (*Engine)(nil)

// discoveryOwner holds the status apart from e.mu, which Start holds during
// lengthy startup work. Reads take only mu; writers serialize on publish so
// events leave in revision order. Lock order: e.mu → publish → mu.
type discoveryOwner struct {
	publish sync.Mutex
	mu      sync.RWMutex
	status  DiscoveryStatus
	attempt uint64
	active  bool
	emitter Emitter
}

// discoveryScope ties a context to one lifetime and, once reconciling, one
// discovery attempt. Promotion starts a new attempt in the same lifetime.
type discoveryScope struct {
	lifetime uint64
	attempt  uint64
}

type discoveryScopeKey struct{}

func scopeOf(ctx context.Context) discoveryScope {
	s, _ := ctx.Value(discoveryScopeKey{}).(discoveryScope)
	return s
}

// DiscoveryStatus returns the current snapshot without waiting on engine mu.
func (e *Engine) DiscoveryStatus(context.Context) (DiscoveryStatus, error) {
	d := &e.discovery
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.status.Lifetime == 0 {
		return DiscoveryStatus{State: DiscoveryInitializing}, nil
	}
	return d.status, nil
}

func (e *Engine) setDiscoveryEmitter(emitter Emitter) {
	e.discovery.mu.Lock()
	e.discovery.emitter = emitter
	e.discovery.mu.Unlock()
}

// beginDiscovery opens a new lifetime and returns ctx scoped to it.
func (e *Engine) beginDiscovery(ctx context.Context) context.Context {
	d := &e.discovery
	d.publish.Lock()
	defer d.publish.Unlock()
	d.mu.Lock()
	configured := 0
	if e.svc != nil {
		configured = len(e.svc.Providers())
	}
	d.status = DiscoveryStatus{
		Lifetime:            d.status.Lifetime + 1,
		Revision:            1,
		State:               DiscoveryInitializing,
		ConfiguredProviders: configured,
	}
	d.attempt = 0
	d.active = true
	d.emitter = e.emitter
	s, emitter := d.status, d.emitter
	d.mu.Unlock()
	emit(emitter, s)
	return context.WithValue(ctx, discoveryScopeKey{}, discoveryScope{lifetime: s.Lifetime})
}

// updateDiscovery applies change if ctx still belongs to the active lifetime
// and, when scoped to an attempt, the current attempt. Late callbacks from a
// closed lifetime or superseded attempt are dropped. newAttempt starts one.
func (e *Engine) updateDiscovery(ctx context.Context, newAttempt bool, change func(*DiscoveryStatus)) (discoveryScope, bool) {
	scope := scopeOf(ctx)
	d := &e.discovery
	d.publish.Lock()
	defer d.publish.Unlock()
	d.mu.Lock()
	if !d.active || scope.lifetime == 0 || d.status.Lifetime != scope.lifetime ||
		(scope.attempt != 0 && scope.attempt != d.attempt) {
		d.mu.Unlock()
		return scope, false
	}
	if newAttempt {
		d.attempt++
		scope.attempt = d.attempt
	}
	change(&d.status)
	d.status.Revision++
	s, emitter := d.status, d.emitter
	d.mu.Unlock()
	emit(emitter, s)
	return scope, true
}

func emit(emitter Emitter, s DiscoveryStatus) {
	if emitter != nil {
		emitter.Emit(DiscoveryStatusEvent, s)
	}
}

// stopDiscovery ends the lifetime: the last snapshot stays readable, and no
// later callback can publish into it or into the next lifetime.
func (e *Engine) stopDiscovery() {
	d := &e.discovery
	d.publish.Lock()
	defer d.publish.Unlock()
	d.mu.Lock()
	d.active = false
	if !d.status.Complete {
		d.status.State = DiscoveryFailed
		d.status.Complete = true
		d.status.Revision++
	}
	d.mu.Unlock()
}

func (e *Engine) discoveryRestoring(ctx context.Context) {
	e.updateDiscovery(ctx, false, func(s *DiscoveryStatus) { s.State = DiscoveryRestoring })
}

// discoveryReconciling marks the catalog usable and starts a discovery
// attempt; pass the returned ctx to the attempt's scan. Call it right after
// the catalog is published.
func (e *Engine) discoveryReconciling(ctx context.Context, mode string) context.Context {
	scope, ok := e.updateDiscovery(ctx, true, func(s *DiscoveryStatus) {
		s.State = DiscoveryReconciling
		s.CacheMode = mode
		s.CatalogUsable = true
		s.Complete = false
		s.DetectionComplete = false
		s.DetectedProviders = 0
		s.FailedProviders = 0
	})
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, discoveryScopeKey{}, scope)
}

// discoverySettledEmpty settles a lifetime with nothing to discover.
func (e *Engine) discoverySettledEmpty(ctx context.Context) {
	ctx = e.discoveryReconciling(ctx, DiscoveryCacheNone)
	e.finishDiscovery(ctx, newDiscoveryAttempt(), nil)
}

// discoveryAttempt collects provider outcomes from one existing discovery
// pass via scan.DiscoveryObserver; it never calls Detect itself.
type discoveryAttempt struct {
	mu       sync.Mutex
	detected map[model.AgentID]bool
	failed   map[model.AgentID]bool
}

func newDiscoveryAttempt() *discoveryAttempt {
	return &discoveryAttempt{detected: map[model.AgentID]bool{}, failed: map[model.AgentID]bool{}}
}

func (a *discoveryAttempt) Detected(id model.AgentID, present bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.detected[id] = present && err == nil
	if err != nil {
		a.failed[id] = true
	}
}

func (a *discoveryAttempt) Scanned(id model.AgentID, err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failed[id] = true
}

// unobservedErrors marks providers that failed before detection, such as
// duplicate IDs the runner refuses. Live-badge errors of detected providers are
// not discovery failures, matching the refresher path.
func (a *discoveryAttempt) unobservedErrors(errs map[model.AgentID]error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range errs {
		if _, seen := a.detected[id]; !seen {
			a.failed[id] = true
		}
	}
}

func (e *Engine) observedDiscovery(ctx context.Context) (context.Context, *discoveryAttempt) {
	a := newDiscoveryAttempt()
	return scan.WithDiscoveryObserver(ctx, a), a
}

// finishDiscovery publishes the attempt's terminal state. err is the pass's
// own error (cancellation); provider errors are isolated and only degrade,
// unless no configured provider answered successfully at all.
func (e *Engine) finishDiscovery(ctx context.Context, a *discoveryAttempt, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e.updateDiscovery(ctx, false, func(s *DiscoveryStatus) {
		s.Complete = true
		s.DetectionComplete = len(a.detected) == s.ConfiguredProviders
		s.DetectedProviders = 0
		healthy := 0
		for id, present := range a.detected {
			if present {
				s.DetectedProviders++
			}
			if !a.failed[id] {
				healthy++
			}
		}
		s.FailedProviders = len(a.failed)
		switch {
		case err != nil, s.ConfiguredProviders > 0 && healthy == 0:
			s.State = DiscoveryFailed
		case s.FailedProviders > 0 || !s.DetectionComplete:
			s.State = DiscoveryDegraded
		default:
			s.State = DiscoverySettled
		}
	})
}

// startRunnerDiscovery owns disabled/fallback bootstrap; dependencies are
// captured under e.mu so an old scan cannot look up a new lifetime's resources.
func (e *Engine) startRunnerDiscovery(ctx context.Context, mode string) {
	if e.runner == nil {
		e.discoverySettledEmpty(ctx)
		return
	}
	ctx = e.discoveryReconciling(ctx, mode)
	runner, svc, events := e.runner, e.svc, e.events
	e.bg.Go(func() {
		sctx, attempt := e.observedDiscovery(ctx)
		done := startupObservation(ctx).begin("bootstrap")
		report := runner.Run(sctx, svc.ScanStates())
		svc.ApplyReport(report)
		svc.ClearTranscripts()
		events.NotifyFullRefresh()
		attempt.unobservedErrors(report.Errors)
		e.finishDiscovery(sctx, attempt, ctx.Err())
		done(ctx.Err() == nil)
	})
}

// runBootstrap is a cache-backed discovery attempt of r.
func (e *Engine) runBootstrap(ctx context.Context, r *index.Refresher) error {
	sctx, attempt := e.observedDiscovery(ctx)
	err := r.Bootstrap(sctx)
	e.svc.ApplyReport(r.Report())
	e.finishDiscovery(sctx, attempt, err)
	return err
}
