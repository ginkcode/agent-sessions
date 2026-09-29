package app

import (
	"context"
	"sort"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// catalogChangedEvent is the Wails event name carrying CatalogChanged.
const catalogChangedEvent = "catalog:changed"

// catalogDebounce is the trailing window that coalesces rapid scan commits.
const catalogDebounce = 100 * time.Millisecond

// CatalogChanged delivers coalesced catalog updates to the Wails UI. An event
// with GroupsDirty set and no refs is a full refresh: the catalog was rebuilt
// wholesale and every view must reload.
type CatalogChanged struct {
	Changed     []model.SessionRef `json:"changed"`
	Removed     []model.SessionRef `json:"removed"`
	GroupsDirty bool               `json:"groupsDirty"`
}

// stopper is the subset of *time.Timer the bus needs; tests substitute it.
type stopper interface{ Stop() bool }

// catalogBus coalesces catalog notifications into at most one emission per
// debounce window. All methods are safe on a nil bus, which drops events.
type catalogBus struct {
	emit      func(CatalogChanged)
	afterFunc func(time.Duration, func()) stopper
	delay     time.Duration

	emitMu sync.Mutex // held across emit so stop can wait out a flush

	mu      sync.Mutex
	changed map[string]model.SessionRef
	removed map[string]model.SessionRef
	full    bool
	timer   stopper
	stopped bool
}

// newCatalogBus emits over the Wails runtime bound to ctx. Contexts without
// the Wails event runtime (tests, headless use) silently drop events.
func newCatalogBus(ctx context.Context) *catalogBus {
	return newCatalogBusWith(func(ev CatalogChanged) {
		if ctx != nil && ctx.Value("events") != nil {
			wruntime.EventsEmit(ctx, catalogChangedEvent, ev)
		}
	}, func(d time.Duration, f func()) stopper {
		return time.AfterFunc(d, f)
	}, catalogDebounce)
}

func newCatalogBusWith(emit func(CatalogChanged), afterFunc func(time.Duration, func()) stopper, delay time.Duration) *catalogBus {
	return &catalogBus{
		emit:      emit,
		afterFunc: afterFunc,
		delay:     delay,
		changed:   make(map[string]model.SessionRef),
		removed:   make(map[string]model.SessionRef),
	}
}

// NoteChanged records changed and removed sessions. A removal supersedes an
// earlier change of the same session within the window; a later change of a
// removed session (it reappeared) supersedes the removal.
func (b *catalogBus) NoteChanged(changed, removed []model.SessionRef) {
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
func (b *catalogBus) NotifyFullRefresh() {
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

// stop cancels any pending emission and waits for an in-flight one, so no
// event is emitted once stop returns.
func (b *catalogBus) stop() {
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

// scheduleLocked starts the window on the first note; later notes join it
// rather than extending it, so a steady stream still emits every window.
func (b *catalogBus) scheduleLocked() {
	if b.timer == nil {
		b.timer = b.afterFunc(b.delay, b.flush)
	}
}

func (b *catalogBus) flush() {
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
		// Refs arrive post-commit without pre-images, so any change may have
		// moved a session between groups or altered counts.
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

	b.emit(ev)
}

func sortedRefs(m map[string]model.SessionRef) []model.SessionRef {
	out := make([]model.SessionRef, 0, len(m))
	for _, ref := range m {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
