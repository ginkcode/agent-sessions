package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

func TestDiagnosticsScopedEncodedNotifications(t *testing.T) {
	b, s, c, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	ctx := context.Background()
	if _, e := c.Initialize(ctx, InitializeRequest{}); e != nil {
		t.Fatal(e)
	}
	a, e := c.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1)
	if e != nil {
		t.Fatal(e)
	}
	events := make(chan cd.Event, 8)
	c.SetEmitter(engine.EmitterFunc(func(name string, payload any) {
		if name == engine.CodeDiagnosticsUpdatedEvent {
			events <- payload.(cd.Event)
		}
	}))
	parts, e := b.m.SnapshotEvents(a.Context)
	if e != nil {
		t.Fatal(e)
	}
	ev := parts[0]
	foreign := ev
	foreign.Context.ConnectionID = "foreign"
	foreign.Snapshot.Context = foreign.Context
	if e = s.Notify(engine.CodeDiagnosticsUpdatedEvent, foreign); e != nil {
		t.Fatal(e)
	}
	if e = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); e != nil {
		t.Fatal(e)
	}
	select {
	case got := <-events:
		if got.Context != a.Context {
			t.Fatal("foreign event delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	revoked, e := c.RevokeCodeDiagnostics(ctx, a.Context)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); e != nil {
		t.Fatal(e)
	}
	parts, e = b.m.SnapshotEvents(revoked.Context)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Notify(engine.CodeDiagnosticsUpdatedEvent, parts[0]); e != nil {
		t.Fatal(e)
	}
	select {
	case got := <-events:
		if got.Context != revoked.Context {
			t.Fatal("stale event delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("revoke event not delivered")
	}
	// Check actual Go JSON escaping and the complete notification envelope. A
	// finding is never split, and an oversized arbitrary backend event disconnects.
	ev = parts[0]
	ev.Snapshot.ResultSets = []cd.ResultSet{{ProviderID: "test", Origin: "checker", Scope: cd.Scope{Kind: "project"}, RunID: "run", Diagnostics: []cd.Diagnostic{{Path: "source.go", Severity: "error", Message: strings.Repeat("<", 180000), Source: "test", Freshness: "uncertain", Basis: cd.SavedBasis{Kind: "saved"}}}}}
	raw, _ := json.Marshal(ev)
	frame, _ := json.Marshal(Notification{JSONRPC: JSONRPCVersion, Method: engine.CodeDiagnosticsUpdatedEvent, Params: raw})
	if len(frame) <= 1<<20 {
		t.Fatal("fixture not oversized after escaping")
	}
	if e = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); e == nil {
		t.Fatal("oversized encoded envelope accepted")
	}
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("oversize did not disconnect")
	}
}

func TestDiagnosticsBoundedSlowPeer(t *testing.T) {
	// No writer goroutine: admission itself must remain bounded and nonblocking.
	s := NewServer(&mockBackend{}, strings.NewReader(""), io.Discard)
	token := cd.Context{ConnectionID: "connection", AttachmentID: "attachment", Generation: "generation"}
	s.diagnostics.owned[token.AttachmentID] = token
	ev := cd.Event{ContractVersion: 1, Context: token, PartCount: 1, Snapshot: cd.Snapshot{Context: token}}
	for i, id := range []string{"one", "two", "three"} {
		ev.UpdateID = id
		e := s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev)
		if (i < 2 && e != nil) || (i == 2 && e == nil) {
			t.Fatal(i, e)
		}
	}
	select {
	case <-s.closed:
	default:
		t.Fatal("overflow did not close")
	}
}

func TestDiagnosticsIdleExpiresOwnership(t *testing.T) {
	b, s, c, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	ctx := context.Background()
	// setupPipePair is already serving, so use EOF/Close above for active expiry;
	// exercise idle expiry with a separate server and the same verified fake.
	_ = c.Close()
	_ = s.Close()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	server := NewServer(b, inR, outW)
	server.SetIdleTimeout(50 * time.Millisecond)
	client := NewClient(outR, inW)
	defer func() { _ = client.Close() }()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	if _, e := client.Initialize(ctx, InitializeRequest{}); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, ErrIdleTimeout) {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle server did not end")
	}
}
