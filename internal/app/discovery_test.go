package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

type discoveryBackendStub struct {
	engine.Backend
	status engine.DiscoveryStatus
	err    error
	read   func()
}

func (b discoveryBackendStub) DiscoveryStatus(context.Context) (engine.DiscoveryStatus, error) {
	if b.read != nil {
		b.read()
	}
	return b.status, b.err
}

func TestDiscoveryReplyRoutes(t *testing.T) {
	local := engine.DiscoveryStatus{Lifetime: 2, Revision: 4, State: engine.DiscoverySettled, Complete: true}
	remote := engine.DiscoveryStatus{Lifetime: 1, Revision: 3, State: engine.DiscoveryReconciling, CatalogUsable: true}
	a := NewApp()
	a.backend = discoveryBackendStub{status: local}
	// No connection: Local, including the same optional backend surface.
	reply, err := a.DiscoveryStatus()
	if err != nil || !reply.Available || reply.Status == nil || *reply.Status != local || reply.Host != "" || reply.Generation != 0 {
		t.Fatalf("local reply=%+v err=%v", reply, err)
	}
	a.conn = &connection{gen: 7, wantHost: "box", state: ConnectionState{Phase: ConnConnected, Host: "box", Generation: 7}, client: discoveryBackendStub{status: remote}}
	reply, err = a.DiscoveryStatus()
	if err != nil || !reply.Available || reply.Status == nil || *reply.Status != remote || reply.Host != "box" || reply.Generation != 7 {
		t.Fatalf("remote reply=%+v err=%v", reply, err)
	}
	// Neither a missing optional surface nor unsupported fabricates completion.
	for _, b := range []engine.Backend{markerBackend{}, discoveryBackendStub{err: engine.ErrUnsupportedAction}} {
		a.conn.client = b
		reply, err = a.DiscoveryStatus()
		if err != nil || reply.Available || reply.Status != nil || reply.Host != "box" {
			t.Fatalf("unavailable=%+v err=%v", reply, err)
		}
		data, err := json.Marshal(reply)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["status"]; ok {
			t.Fatalf("unknown status must be omitted: %s", data)
		}
	}
	// Local old/mock backends get the same unavailable result.
	a.conn = nil
	a.backend = markerBackend{}
	if reply, err := a.DiscoveryStatus(); err != nil || reply.Available || reply.Status != nil {
		t.Fatalf("local unavailable=%+v, %v", reply, err)
	}
	// Once a host has been selected, offline must not fall back to Local.
	a.backend = discoveryBackendStub{status: local}
	a.conn = &connection{wantHost: "box", localActive: false}
	if _, err := a.DiscoveryStatus(); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("offline error=%v", err)
	}
	// One captured route: a reply identifies the host that answered even if
	// switching occurs while the backend is reading (ST-03 can discard it).
	a.conn = &connection{gen: 7, wantHost: "box", state: ConnectionState{Phase: ConnConnected, Host: "box", Generation: 7}}
	a.conn.client = discoveryBackendStub{status: remote, read: func() {
		a.conn.gen = 8
		a.conn.state = ConnectionState{Phase: ConnConnected, Host: "other", Generation: 8}
	}}
	reply, err = a.DiscoveryStatus()
	if err != nil || reply.Host != "box" || reply.Generation != 7 {
		t.Fatalf("captured route=%+v err=%v", reply, err)
	}
}

func TestDiscoveryEventHostIsolation(t *testing.T) {
	c := &connection{localActive: true}
	var events []DiscoveryStatusEvent
	inner := engine.EmitterFunc(func(name string, payload any) {
		if name != engine.DiscoveryStatusEvent {
			t.Fatalf("event name=%q", name)
		}
		events = append(events, payload.(DiscoveryStatusEvent))
	})
	local := localOnlyEmitter(inner, c)
	status := engine.DiscoveryStatus{Lifetime: 1, Revision: 3, State: engine.DiscoveryReconciling}
	local.Emit(engine.DiscoveryStatusEvent, status)
	if len(events) != 1 || events[0].Host != "" || events[0].Generation != 0 || events[0].DiscoveryStatus != status {
		t.Fatalf("local=%+v", events)
	}
	c.gen, c.wantHost, c.localActive = 4, "box", false
	c.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 4}
	c.client = markerBackend{}
	remote := generationEmitter(inner, 4, c)
	local.Emit(engine.DiscoveryStatusEvent, status)
	remote.Emit(engine.DiscoveryStatusEvent, status)
	if len(events) != 2 || events[1].Host != "box" || events[1].Generation != 4 || events[1].DiscoveryStatus != status {
		t.Fatalf("remote=%+v", events)
	}
	// A superseded host cannot land in the next host's view.
	c.gen, c.wantHost = 5, "other"
	c.state = ConnectionState{Phase: ConnConnected, Host: "other", Generation: 5}
	remote.Emit(engine.DiscoveryStatusEvent, status)
	local.Emit(engine.DiscoveryStatusEvent, status)
	if len(events) != 2 {
		t.Fatalf("stale events=%+v", events)
	}
	generationEmitter(inner, 5, c).Emit(engine.DiscoveryStatusEvent, status)
	if len(events) != 3 || events[2].Host != "other" || events[2].Generation != 5 {
		t.Fatalf("other=%+v", events)
	}
	// Return to Local, then reject a delayed remote event.
	c.gen, c.wantHost, c.localActive, c.client = 6, "", true, nil
	c.state = ConnectionState{Phase: ConnLocal, Generation: 6}
	generationEmitter(inner, 5, c).Emit(engine.DiscoveryStatusEvent, status)
	local.Emit(engine.DiscoveryStatusEvent, status)
	if len(events) != 4 || events[3].Host != "" || events[3].Generation != 0 {
		t.Fatalf("back to local=%+v", events)
	}
}
