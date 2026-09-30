package app

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// fakeTimers captures scheduled callbacks so tests fire windows explicitly.
type fakeTimers struct {
	mu      sync.Mutex
	pending []*fakeTimer
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped
	t.stopped = true
	return was
}

func (ft *fakeTimers) afterFunc(_ time.Duration, f func()) stopper {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{f: f}
	ft.pending = append(ft.pending, t)
	return t
}

// fire runs every scheduled, unstopped callback and reports how many ran.
func (ft *fakeTimers) fire() int {
	ft.mu.Lock()
	timers := ft.pending
	ft.pending = nil
	ft.mu.Unlock()
	n := 0
	for _, t := range timers {
		if !t.stopped {
			t.f()
			n++
		}
	}
	return n
}

type recorder struct {
	mu     sync.Mutex
	events []CatalogChanged
}

func (r *recorder) emit(ev CatalogChanged) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) all() []CatalogChanged {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CatalogChanged(nil), r.events...)
}

func newTestBus() (*catalogBus, *fakeTimers, *recorder) {
	ft := &fakeTimers{}
	rec := &recorder{}
	return newCatalogBusWith(rec.emit, ft.afterFunc, catalogDebounce), ft, rec
}

func busRef(id string) model.SessionRef {
	return model.SessionRef{Agent: model.AgentClaude, ID: id}
}

func TestCatalogBus_Coalesce(t *testing.T) {
	bus, ft, rec := newTestBus()
	bus.NoteChanged([]model.SessionRef{busRef("b")}, nil)
	bus.NoteChanged([]model.SessionRef{busRef("a")}, nil)
	bus.NoteChanged(nil, []model.SessionRef{busRef("c")})

	if len(ft.pending) != 1 {
		t.Fatalf("scheduled %d timers, want 1 per window", len(ft.pending))
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("emitted before the window closed: %+v", got)
	}
	ft.fire()

	got := rec.all()
	want := []CatalogChanged{{
		Changed:     []model.SessionRef{busRef("a"), busRef("b")},
		Removed:     []model.SessionRef{busRef("c")},
		GroupsDirty: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}

	// The next note opens a fresh window.
	bus.NoteChanged([]model.SessionRef{busRef("d")}, nil)
	if ft.fire() != 1 || len(rec.all()) != 2 {
		t.Fatalf("second window did not emit: %+v", rec.all())
	}
}

func TestCatalogBus_IgnoresEmptyNotes(t *testing.T) {
	bus, ft, rec := newTestBus()
	bus.NoteChanged(nil, nil)
	if len(ft.pending) != 0 {
		t.Fatal("empty note scheduled a window")
	}
	ft.fire()
	if len(rec.all()) != 0 {
		t.Fatalf("empty note emitted: %+v", rec.all())
	}
}

func TestCatalogBus_DedupeAndRemovalPrecedence(t *testing.T) {
	bus, ft, rec := newTestBus()
	bus.NoteChanged([]model.SessionRef{busRef("a"), busRef("a"), busRef("b")}, nil)
	bus.NoteChanged([]model.SessionRef{busRef("b")}, nil)
	bus.NoteChanged(nil, []model.SessionRef{busRef("a"), busRef("a")})
	ft.fire()

	got := rec.all()
	want := []CatalogChanged{{
		Changed:     []model.SessionRef{busRef("b")},
		Removed:     []model.SessionRef{busRef("a")},
		GroupsDirty: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
}

func TestCatalogBus_ReappearedSessionIsChanged(t *testing.T) {
	bus, ft, rec := newTestBus()
	bus.NoteChanged(nil, []model.SessionRef{busRef("a")})
	bus.NoteChanged([]model.SessionRef{busRef("a")}, nil)
	ft.fire()

	got := rec.all()
	if len(got) != 1 || !reflect.DeepEqual(got[0].Changed, []model.SessionRef{busRef("a")}) || len(got[0].Removed) != 0 {
		t.Fatalf("events = %+v, want a single change of a", got)
	}
}

func TestCatalogBus_FullRefreshSentinel(t *testing.T) {
	bus, ft, rec := newTestBus()
	bus.NoteChanged([]model.SessionRef{busRef("a")}, []model.SessionRef{busRef("b")})
	bus.NotifyFullRefresh()
	ft.fire()

	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ev := got[0]
	if !ev.GroupsDirty || len(ev.Changed) != 0 || len(ev.Removed) != 0 {
		t.Fatalf("full refresh = %+v, want groupsDirty with no refs", ev)
	}

	// The full flag resets after emission.
	bus.NoteChanged([]model.SessionRef{busRef("c")}, nil)
	ft.fire()
	if got := rec.all(); len(got) != 2 || len(got[1].Changed) != 1 {
		t.Fatalf("follow-up event = %+v, want a delta", got)
	}
}

func TestCatalogBus_StopStopsEmission(t *testing.T) {
	bus, ft, rec := newTestBus()
	bus.NoteChanged([]model.SessionRef{busRef("a")}, nil)
	bus.Stop()
	if !ft.pending[0].stopped {
		t.Error("stop did not cancel the pending timer")
	}

	// A callback that raced past Stop must still not emit.
	ft.pending[0].f()
	bus.NoteChanged([]model.SessionRef{busRef("b")}, nil)
	bus.NotifyFullRefresh()
	ft.fire()
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("emitted after stop: %+v", got)
	}
}

func TestCatalogBus_NilSafe(t *testing.T) {
	var bus *catalogBus
	bus.NoteChanged([]model.SessionRef{busRef("a")}, nil)
	bus.NotifyFullRefresh()
	bus.Stop()
}

func TestCatalogBus_RealTimerEmits(t *testing.T) {
	done := make(chan CatalogChanged, 1)
	bus := newCatalogBusWith(func(ev CatalogChanged) { done <- ev }, func(d time.Duration, f func()) stopper {
		return time.AfterFunc(d, f)
	}, time.Millisecond)
	defer bus.Stop()

	bus.NoteChanged([]model.SessionRef{busRef("a")}, nil)
	select {
	case ev := <-done:
		if len(ev.Changed) != 1 {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timer never emitted")
	}
}
