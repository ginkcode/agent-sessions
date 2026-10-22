package rpc

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

func receiveDiagnostics(t *testing.T, events <-chan cd.Event) cd.Event {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("diagnostics part not delivered")
		return cd.Event{}
	}
}

func diagnosticsEvents(c *Client) <-chan cd.Event {
	events := make(chan cd.Event, 128)
	c.SetEmitter(engine.EmitterFunc(func(name string, payload any) {
		if name == engine.CodeDiagnosticsUpdatedEvent {
			events <- payload.(cd.Event)
		}
	}))
	return events
}

func attachDiagnostics(t *testing.T, b *diagnosticsBackend, c *Client) engine.CodeDiagnosticsAttachment {
	t.Helper()
	ctx := context.Background()
	if _, err := c.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	a, err := c.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDiagnosticsRPCMultipartPreservesParts(t *testing.T) {
	b, s, c, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	a := attachDiagnostics(t, b, c)
	events := diagnosticsEvents(c)
	// Transport forwards parts, including duplicates and same-sequence out-of-order
	// parts. It must not manufacture a partial snapshot or deduplicate by sequence.
	first := cd.Event{ContractVersion: 1, Context: a.Context, Sequence: a.Snapshot.Sequence, UpdateID: "multipart", PartCount: 2, Snapshot: a.Snapshot}
	first.Snapshot.ResultSets = []cd.ResultSet{{ProviderID: "test", Origin: "checker", Scope: cd.Scope{Kind: "project"}, RunID: "run", Diagnostics: []cd.Diagnostic{{Path: "a.go", Severity: "error", Message: "<&> first", Source: "test", Freshness: "uncertain", Basis: cd.SavedBasis{Kind: "saved"}}}}}
	second := first
	second.PartIndex = 1
	second.Snapshot = a.Snapshot
	second.Snapshot.ResultSets = []cd.ResultSet{{ProviderID: "other", Origin: "checker", Scope: cd.Scope{Kind: "project"}, RunID: "run", Diagnostics: []cd.Diagnostic{}}}
	for _, want := range []cd.Event{second, first, second} {
		if err := s.Notify(engine.CodeDiagnosticsUpdatedEvent, want); err != nil {
			t.Fatal(err)
		}
		if got := receiveDiagnostics(t, events); !reflect.DeepEqual(got, want) {
			t.Fatalf("part changed across RPC: got %#v want %#v", got, want)
		}
	}
}

func TestDiagnosticsRPCIncompleteAndSequenceGapResnapshot(t *testing.T) {
	for _, missing := range []bool{true, false} {
		name := "sequence-gap"
		if missing {
			name = "missing-part"
		}
		t.Run(name, func(t *testing.T) {
			b, s, c, cleanup := rpcDiagnosticsFixture(t)
			defer cleanup()
			a := attachDiagnostics(t, b, c)
			events := diagnosticsEvents(c)
			ctx := context.Background()
			// Manager state advances without publishing the intermediate update. This
			// models the manager's replacement-after-overflow path, not network loss.
			older, err := c.ApproveCodeDiagnostics(ctx, a.Context, "session", a.Snapshot.Proposal.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			latest, err := c.ApproveCodeDiagnostics(ctx, a.Context, "session", a.Snapshot.Proposal.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			parts, err := b.m.SnapshotEvents(a.Context)
			if err != nil || len(parts) != 1 {
				t.Fatal(parts, err)
			}
			ev := parts[0]
			if missing {
				ev.Sequence, ev.Snapshot = older.Sequence, older
				ev.UpdateID, ev.PartCount = "incomplete", 2 // deliberately never send part 1
			}
			if err = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); err != nil {
				t.Fatal(err)
			}
			got := receiveDiagnostics(t, events)
			if !reflect.DeepEqual(got, ev) {
				t.Fatal("recovery metadata or part changed")
			}
			if missing && got.PartCount != 2 {
				t.Fatal("incomplete update disguised as complete")
			}
			if !missing && got.Sequence <= a.Snapshot.Sequence+1 {
				t.Fatal("sequence gap hidden")
			}
			// A consumer detects the metadata above and issues this actual RPC. No
			// frontend assembly/retry policy is implemented by this test or the client.
			recovered, err := c.GetCodeDiagnosticsSnapshot(ctx, a.Context)
			if err != nil || !reflect.DeepEqual(recovered, latest) {
				t.Fatalf("resnapshot did not return latest complete state: %v", err)
			}
			// An older queued part must not roll back the client's accepted sequence.
			if !missing {
				stale := ev
				stale.Sequence, stale.Snapshot = older.Sequence, older
				if err = s.Notify(engine.CodeDiagnosticsUpdatedEvent, stale); err != nil {
					t.Fatal(err)
				}
				if err = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); err != nil {
					t.Fatal(err)
				}
				if got = receiveDiagnostics(t, events); got.Sequence != latest.Sequence {
					t.Fatal("older sequence escaped transport guard")
				}
			}
		})
	}
}

// Pauses a real notification write after the initialize/attach RPCs. Close
// releases the writer, so overflow cannot deadlock Serve or test cleanup.
type diagnosticsBlockedWriter struct {
	io.WriteCloser
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (w *diagnosticsBlockedWriter) Write(p []byte) (int, error) {
	// Only notifications are gated; request/reply handshake uses the same pipe.
	var n Notification
	if json.Unmarshal(p, &n) == nil && n.Method == engine.CodeDiagnosticsUpdatedEvent {
		w.once.Do(func() { close(w.entered) })
		<-w.closed
		return 0, io.ErrClosedPipe
	}
	return w.WriteCloser.Write(p)
}

func (w *diagnosticsBlockedWriter) Close() error {
	// Server.Close is once-only, but underlying handles may also be cleaned up.
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	return w.WriteCloser.Close()
}

func TestDiagnosticsRPCOverflowDisconnectAndReattach(t *testing.T) {
	var blocked *diagnosticsBlockedWriter
	b, s, c, cleanup := rpcDiagnosticsTransportFixture(t, func(w io.WriteCloser) io.WriteCloser {
		blocked = &diagnosticsBlockedWriter{WriteCloser: w, entered: make(chan struct{}), closed: make(chan struct{})}
		return blocked
	})
	defer cleanup()
	a := attachDiagnostics(t, b, c)
	ctx := context.Background()
	if _, err := c.ApproveCodeDiagnostics(ctx, a.Context, "session", a.Snapshot.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunCodeDiagnostics(ctx, a.Context, nil); err != nil {
		t.Fatal(err)
	}
	active := <-b.fake.started
	parts, err := b.m.SnapshotEvents(a.Context)
	if err != nil {
		t.Fatal(err)
	}
	ev := parts[0]
	if err = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("writer not blocked")
	}
	// The first write is in flight; exactly 64 further parts fit the queue.
	for i := 0; i < 64; i++ {
		if err = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
	}
	if err = s.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); err == nil {
		t.Fatal("overflow silently accepted")
	}
	select {
	case <-s.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow did not close connection")
	}
	select {
	case <-active.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("overflow did not expire owned work")
	}
	if _, err = c.GetCodeDiagnosticsSnapshot(ctx, a.Context); err == nil {
		t.Fatal("disconnected snapshot succeeded")
	}
	// Reconnect uses a fresh owner and approval, never a reused attachment/grant.
	next, nextServer, nextClient, nextCleanup := rpcDiagnosticsFixture(t)
	defer nextCleanup()
	// Same authenticated host and canonical root, but a fresh connection owner.
	next.root, next.host = b.root, b.host
	fresh := attachDiagnostics(t, next, nextClient)
	if fresh.Context == a.Context || fresh.Snapshot.Approval.State == "approved" {
		t.Fatal("reconnect reused ownership or grant")
	}
	var out cd.Snapshot
	if err = nextClient.call(ctx, "getCodeDiagnosticsSnapshot", codeDiagnosticsParams{Context: a.Context}, &out); err == nil {
		t.Fatal("old connection token accepted")
	}
	events := diagnosticsEvents(nextClient)
	if err = nextServer.Notify(engine.CodeDiagnosticsUpdatedEvent, ev); err != nil {
		t.Fatal(err)
	}
	current, err := next.m.SnapshotEvents(fresh.Context)
	if err != nil {
		t.Fatal(err)
	}
	if err = nextServer.Notify(engine.CodeDiagnosticsUpdatedEvent, current[0]); err != nil {
		t.Fatal(err)
	}
	if got := receiveDiagnostics(t, events); got.Context != fresh.Context {
		t.Fatal("late old connection event delivered")
	}
	recovered, err := nextClient.GetCodeDiagnosticsSnapshot(ctx, fresh.Context)
	if err != nil || !reflect.DeepEqual(recovered, fresh.Snapshot) {
		t.Fatal("fresh resnapshot failed", err)
	}
}
