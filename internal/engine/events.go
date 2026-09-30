package engine

import (
	"sort"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// Event names emitted across the engine boundary.
const (
	CatalogChangedEvent = "catalog:changed"
	IndexProgressEvent  = "index:progress"
)

// catalogDebounce is the trailing window that coalesces rapid scan commits.
const catalogDebounce = 100 * time.Millisecond

// Emitter sends named events to subscribers (e.g. desktop UI or JSON-RPC notifications).
type Emitter interface {
	Emit(name string, payload any)
}

// EmitterFunc adapts a plain function to the Emitter interface.
type EmitterFunc func(name string, payload any)

// Emit implements Emitter.
func (f EmitterFunc) Emit(name string, payload any) {
	if f != nil {
		f(name, payload)
	}
}

// NopEmitter drops all emitted events.
type NopEmitter struct{}

// Emit implements Emitter.
func (NopEmitter) Emit(string, any) {}

// CatalogChanged delivers coalesced catalog updates. An event with GroupsDirty
// set and no refs is a full refresh: the catalog was rebuilt wholesale and every
// view must reload.
type CatalogChanged struct {
	Changed     []model.SessionRef `json:"changed"`
	Removed     []model.SessionRef `json:"removed"`
	GroupsDirty bool               `json:"groupsDirty"`
}

// Stopper is the subset of *time.Timer the bus needs; tests substitute it.
type Stopper interface {
	Stop() bool
}

// CatalogBus coalesces catalog notifications into at most one emission per
// debounce window. All methods are safe on a nil bus, which drops events.
type CatalogBus struct {
	emitter   Emitter
	afterFunc func(time.Duration, func()) Stopper
	delay     time.Duration

	emitMu sync.Mutex // held across emit so stop can wait out a flush

	mu      sync.Mutex
	changed map[string]model.SessionRef
	removed map[string]model.SessionRef
	full    bool
	timer   Stopper
	stopped bool
}

// NewCatalogBus creates a bus that emits over emitter with the default 100ms debounce.
func NewCatalogBus(emitter Emitter) *CatalogBus {
	if emitter == nil {
		emitter = NopEmitter{}
	}
	return NewCatalogBusWith(emitter, func(d time.Duration, f func()) Stopper {
		return time.AfterFunc(d, f)
	}, catalogDebounce)
}

// NewCatalogBusWith allows custom timers and debounce delay (for tests).
func NewCatalogBusWith(emitter Emitter, afterFunc func(time.Duration, func()) Stopper, delay time.Duration) *CatalogBus {
	if emitter == nil {
		emitter = NopEmitter{}
	}
	return &CatalogBus{
		emitter:   emitter,
		afterFunc: afterFunc,
		delay:     delay,
		changed:   make(map[string]model.SessionRef),
		removed:   make(map[string]model.SessionRef),
	}
}

// NoteChanged records changed and removed sessions. A removal supersedes an
// earlier change of the same session within the window; a later change of a
// removed session (it reappeared) supersedes the removal.
func (b *CatalogBus) NoteChanged(changed, removed []model.SessionRef) {
	if b == nil || (len(changed) == 0 && len(removed) == 0) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	for _, ref := range changed {
		k := ref.Key()
		delete(b.removed, k)
		b.changed[k] = ref
	}
	for _, ref := range removed {
		k := ref.Key()
		delete(b.changed, k)
		b.removed[k] = ref
	}
	b.scheduleLocked()
}

// NotifyFullRefresh records a wholesale catalog rebuild. It supersedes any
// pending refs, since the UI reloads everything anyway.
func (b *CatalogBus) NotifyFullRefresh() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	b.full = true
	b.scheduleLocked()
}

// Stop cancels any pending emission and waits for an in-flight one, so no
// event is emitted once Stop returns.
func (b *CatalogBus) Stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.stopped = true
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()

	b.emitMu.Lock()
	defer b.emitMu.Unlock()
}

func (b *CatalogBus) stop() {
	b.Stop()
}

func (b *CatalogBus) scheduleLocked() {
	if b.timer == nil && b.afterFunc != nil {
		b.timer = b.afterFunc(b.delay, b.flush)
	}
}

func (b *CatalogBus) flush() {
	b.emitMu.Lock()
	defer b.emitMu.Unlock()

	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	var ev CatalogChanged
	switch {
	case b.full:
		ev = CatalogChanged{GroupsDirty: true}
	case len(b.changed) > 0 || len(b.removed) > 0:
		ev = CatalogChanged{
			Changed:     sortedRefs(b.changed),
			Removed:     sortedRefs(b.removed),
			GroupsDirty: true,
		}
	default:
		b.timer = nil
		b.mu.Unlock()
		return
	}
	b.changed = make(map[string]model.SessionRef)
	b.removed = make(map[string]model.SessionRef)
	b.full = false
	b.timer = nil
	b.mu.Unlock()

	b.emitter.Emit(CatalogChangedEvent, ev)
}

func sortedRefs(m map[string]model.SessionRef) []model.SessionRef {
	out := make([]model.SessionRef, 0, len(m))
	for _, ref := range m {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
