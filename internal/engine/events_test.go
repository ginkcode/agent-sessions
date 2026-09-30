package engine

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

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

func (ft *fakeTimers) afterFunc(_ time.Duration, f func()) Stopper {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{f: f}
	ft.pending = append(ft.pending, t)
	return t
}

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

func (r *recorder) Emit(name string, payload any) {
	if name != CatalogChangedEvent {
		return
	}
	ev, ok := payload.(CatalogChanged)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) get() []CatalogChanged {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CatalogChanged, len(r.events))
	copy(out, r.events)
	return out
}

func TestCatalogBusCoalescesWithinWindow(t *testing.T) {
	ft := &fakeTimers{}
	rec := &recorder{}
	bus := NewCatalogBusWith(rec, ft.afterFunc, time.Second)

	refA := model.SessionRef{Agent: "claude-code", ID: "a"}
	refB := model.SessionRef{Agent: "claude-code", ID: "b"}
	refC := model.SessionRef{Agent: "claude-code", ID: "c"}

	bus.NoteChanged([]model.SessionRef{refA}, nil)
	bus.NoteChanged([]model.SessionRef{refB}, []model.SessionRef{refC})

	if len(rec.get()) != 0 {
		t.Fatalf("emitted before flush")
	}

	if n := ft.fire(); n != 1 {
		t.Fatalf("expected 1 timer fired, got %d", n)
	}

	evs := rec.get()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if !ev.GroupsDirty {
		t.Errorf("expected GroupsDirty=true")
	}
	if !reflect.DeepEqual(ev.Changed, []model.SessionRef{refA, refB}) {
		t.Errorf("wrong changed: %+v", ev.Changed)
	}
	if !reflect.DeepEqual(ev.Removed, []model.SessionRef{refC}) {
		t.Errorf("wrong removed: %+v", ev.Removed)
	}
}

func TestCatalogBusRemovalSupersedesChange(t *testing.T) {
	ft := &fakeTimers{}
	rec := &recorder{}
	bus := NewCatalogBusWith(rec, ft.afterFunc, time.Second)

	ref := model.SessionRef{Agent: "claude-code", ID: "x"}
	bus.NoteChanged([]model.SessionRef{ref}, nil)
	bus.NoteChanged(nil, []model.SessionRef{ref})
	ft.fire()

	evs := rec.get()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if len(evs[0].Changed) != 0 {
		t.Errorf("expected no changed, got %+v", evs[0].Changed)
	}
	if !reflect.DeepEqual(evs[0].Removed, []model.SessionRef{ref}) {
		t.Errorf("expected removed [%+v], got %+v", ref, evs[0].Removed)
	}
}

func TestCatalogBusFullRefreshSupersedesRefs(t *testing.T) {
	ft := &fakeTimers{}
	rec := &recorder{}
	bus := NewCatalogBusWith(rec, ft.afterFunc, time.Second)

	ref := model.SessionRef{Agent: "claude-code", ID: "x"}
	bus.NoteChanged([]model.SessionRef{ref}, nil)
	bus.NotifyFullRefresh()
	ft.fire()

	evs := rec.get()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if !ev.GroupsDirty || len(ev.Changed) != 0 || len(ev.Removed) != 0 {
		t.Errorf("expected bare full refresh, got %+v", ev)
	}
}

func TestCatalogBusStopSuppressesEmission(t *testing.T) {
	ft := &fakeTimers{}
	rec := &recorder{}
	bus := NewCatalogBusWith(rec, ft.afterFunc, time.Second)

	ref := model.SessionRef{Agent: "claude-code", ID: "x"}
	bus.NoteChanged([]model.SessionRef{ref}, nil)
	bus.Stop()
	ft.fire()

	if len(rec.get()) != 0 {
		t.Fatalf("emitted after stop")
	}
}

func TestCatalogBusNilSafe(t *testing.T) {
	var bus *CatalogBus
	bus.NoteChanged([]model.SessionRef{{Agent: "claude-code", ID: "a"}}, nil)
	bus.NotifyFullRefresh()
	bus.Stop()
}
