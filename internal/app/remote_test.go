package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func TestConnection_LocalByDefault(t *testing.T) {
	c := newConnection(nil)
	if got := c.snapshot().Phase; got != ConnLocal {
		t.Fatalf("phase = %q", got)
	}
	if c.backend(nil) != nil {
		t.Fatal("local phase should not report a remote backend")
	}
}

func TestConnection_RejectsBadAlias(t *testing.T) {
	c := newConnection(nil)
	if err := c.Connect(context.Background(), "-oProxyCommand=x"); err == nil {
		t.Fatal("expected invalid alias")
	}
	if c.snapshot().Phase != ConnLocal {
		t.Fatal("failed connect changed phase")
	}
}

func TestConnection_ConnectThenDisconnect(t *testing.T) {
	var events []string
	var mu sync.Mutex
	c := newConnection(engine.EmitterFunc(func(name string, payload any) {
		st := payload.(ConnectionState)
		mu.Lock()
		events = append(events, st.Phase)
		mu.Unlock()
	}))
	c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
		t.Fatal("dial should not be the real one; test uses a stub session path")
		return nil, errors.New("unreachable")
	}

	// A dialer that fails once lets us observe disconnected without a session.
	failed := make(chan struct{})
	c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
		select {
		case <-failed:
		default:
			close(failed)
		}
		return nil, errors.New("ssh: refused")
	}
	prev := reconnectBackoff
	reconnectBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { reconnectBackoff = prev })

	if err := c.Connect(context.Background(), "box"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("dial was not attempted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.snapshot().Phase == ConnReconnecting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := c.snapshot()
	if st.Phase != ConnReconnecting || st.Host != "box" || st.Error == "" {
		t.Fatalf("state = %+v", st)
	}
	// Still local as the active backend: handshake never completed.
	if c.backend(markerBackend{}) == nil {
		t.Fatal("expected local backend while not connected")
	}

	c.Disconnect()
	if got := c.snapshot().Phase; got != ConnLocal {
		t.Fatalf("after disconnect phase = %q", got)
	}
	if c.snapshot().Host != "" {
		t.Fatal("host not cleared")
	}
}

func TestConnection_ConnectedUsesRemoteBackend(t *testing.T) {
	prev := reconnectBackoff
	reconnectBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { reconnectBackoff = prev })

	c := newConnection(nil)
	released := make(chan struct{})
	c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
		client, init := pipeClient(t)
		s := remote.NewTestSession(alias, client, init)
		go func() {
			select {
			case <-released:
				_ = s.Close()
			case <-ctx.Done():
			}
		}()
		return s, nil
	}
	if err := c.Connect(context.Background(), "box"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.snapshot().Phase != ConnConnected {
		time.Sleep(10 * time.Millisecond)
	}
	st := c.snapshot()
	if st.Phase != ConnConnected || !st.Capabilities.Search || st.Generation == 0 {
		t.Fatalf("state = %+v", st)
	}
	if c.backend(markerBackend{}) == nil {
		t.Fatal("connected backend is nil")
	}
	if _, ok := c.backend(markerBackend{}).(markerBackend); ok {
		t.Fatal("connected backend fell through to local")
	}

	close(released)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.snapshot().Phase != ConnReconnecting {
		time.Sleep(10 * time.Millisecond)
	}
	if c.snapshot().Phase != ConnReconnecting {
		t.Fatalf("after drop phase = %q", c.snapshot().Phase)
	}
	c.Disconnect()
}

func pipeClient(t *testing.T) (*rpc.Client, *rpc.InitializeResult) {
	t.Helper()
	r, w := io.Pipe()
	client := rpc.NewClient(r, w)
	res := &rpc.InitializeResult{
		ProtocolVersion: rpc.ProtocolVersion,
		AppVersion:      "dev",
		Capabilities:    rpc.Capabilities{Search: true},
	}
	return client, res
}

func TestConnection_StaleGenerationDropped(t *testing.T) {
	c := newConnection(nil)
	c.gen = 2
	if c.setPhase(1, ConnConnected, "old", "", rpc.Capabilities{}, "") {
		t.Fatal("stale generation applied")
	}
	if c.snapshot().Phase != ConnLocal {
		t.Fatalf("phase = %q", c.snapshot().Phase)
	}
	if !c.setPhase(2, ConnDisconnected, "box", "lost", rpc.Capabilities{}, "") {
		t.Fatal("current generation rejected")
	}
	if c.snapshot().Phase != ConnDisconnected {
		t.Fatal(c.snapshot().Phase)
	}
}

func TestConnection_ShutdownBlocksConnect(t *testing.T) {
	c := newConnection(nil)
	c.Shutdown()
	if err := c.Connect(context.Background(), "box"); err == nil {
		t.Fatal("expected shutdown error")
	}
}

// markerBackend is a non-nil Backend stand-in. backend() only returns it when
// no remote session is active, and the test never calls its methods.
type markerBackend struct{ engine.Backend }
