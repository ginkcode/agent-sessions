package engine

import (
	"context"
	"sync"
	"time"
)

// StartupSample contains only phase names, monotonic process-local timings and
// success. It deliberately contains no roots, errors or session data.
type StartupSample struct {
	Phase    string
	Started  time.Duration
	Finished time.Duration
	Success  bool
}

// StartupObservation is an opt-in, concurrency-safe collector. Use a fresh
// collector per startup; its origin is local to this process, never a wire clock.
// Collection has no callbacks, logging, persisted data or default global hook.
type StartupObservation struct {
	origin  time.Time
	mu      sync.Mutex
	samples []StartupSample
	first   map[string]bool
}

func NewStartupObservation() *StartupObservation {
	return &StartupObservation{origin: time.Now(), first: make(map[string]bool)}
}

type startupObservationKey struct{}

// WithStartupObservation opts this startup context into observation.
func WithStartupObservation(ctx context.Context, o *StartupObservation) context.Context {
	return context.WithValue(ctx, startupObservationKey{}, o)
}

func startupObservation(ctx context.Context) *StartupObservation {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(startupObservationKey{}).(*StartupObservation)
	return o
}

// BeginStartupPhase records an opt-in phase on this process's context-local
// observer. It is a no-op without observation; callers must use content-free names.
func BeginStartupPhase(ctx context.Context, phase string) func(bool) {
	return startupObservation(ctx).begin(phase)
}

func (o *StartupObservation) begin(phase string) func(bool) {
	if o == nil {
		return func(bool) {}
	}
	start := time.Since(o.origin)
	return func(ok bool) {
		sample := StartupSample{phase, start, time.Since(o.origin), ok}
		o.mu.Lock()
		defer o.mu.Unlock()
		// Query observations describe only the first successful response.
		switch phase {
		case "groups", "counts", "sessions", "client_groups", "client_counts", "client_sessions":
			if !ok || o.first[phase] {
				return
			}
			o.first[phase] = true
		}
		o.samples = append(o.samples, sample)
	}
}

// Samples returns a detached snapshot in completion order (not phase order).
func (o *StartupObservation) Samples() []StartupSample {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]StartupSample(nil), o.samples...)
}
