package app

import (
	"context"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// catalogChangedEvent is the Wails event name carrying CatalogChanged.
const catalogChangedEvent = engine.CatalogChangedEvent

// catalogDebounce is the trailing window that coalesces rapid scan commits.
const catalogDebounce = 100 * time.Millisecond

// CatalogChanged delivers coalesced catalog updates to the Wails UI.
type CatalogChanged = engine.CatalogChanged

// Emitter sends named events to subscribers.
type Emitter = engine.Emitter

// stopper is the subset of *time.Timer the bus needs; tests substitute it.
type stopper = engine.Stopper

// catalogBus aliases engine.CatalogBus.
type catalogBus = engine.CatalogBus

// WailsEmitter implements engine.Emitter by forwarding to Wails EventsEmit.
type WailsEmitter struct {
	ctx context.Context
}

// NewWailsEmitter creates an emitter backed by wruntime.EventsEmit.
func NewWailsEmitter(ctx context.Context) engine.Emitter {
	return &WailsEmitter{ctx: ctx}
}

// Emit sends the event over Wails if ctx has the events runtime available.
func (w *WailsEmitter) Emit(name string, payload any) {
	if w != nil && w.ctx != nil && w.ctx.Value("events") != nil {
		wruntime.EventsEmit(w.ctx, name, payload)
	}
}

// newCatalogBus creates a bus that emits over Wails runtime in ctx.
func newCatalogBus(ctx context.Context) *catalogBus {
	return engine.NewCatalogBus(NewWailsEmitter(ctx))
}

// newCatalogBusWith allows custom emission callback and timer (for tests).
func newCatalogBusWith(emit func(CatalogChanged), afterFunc func(time.Duration, func()) stopper, delay time.Duration) *catalogBus {
	emitter := engine.EmitterFunc(func(name string, payload any) {
		if ev, ok := payload.(CatalogChanged); ok && emit != nil {
			emit(ev)
		}
	})
	return engine.NewCatalogBusWith(emitter, afterFunc, delay)
}
