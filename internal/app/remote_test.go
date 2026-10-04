package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
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
	// Dropped: the host stays selected and calls fail, never going to Local.
	r := c.route()
	if _, ok := r.backend.(offlineBackend); !ok || r.host != "box" {
		t.Fatalf("after drop route = %+v", r)
	}
	c.Disconnect()
	if r := c.route(); r.backend != nil || r.host != "" {
		t.Fatalf("after disconnect route = %+v", r)
	}
}

// localRecorder is a Local backend that records the calls that reach it.
type localRecorder struct {
	engine.Backend
	mu    sync.Mutex
	calls []string
}

func (l *localRecorder) record(name string) {
	l.mu.Lock()
	l.calls = append(l.calls, name)
	l.mu.Unlock()
}

func (l *localRecorder) DeleteSessions(context.Context, []model.SessionRef, string) (engine.DeleteReport, error) {
	l.record("DeleteSessions")
	return engine.DeleteReport{}, nil
}

func (l *localRecorder) SetAllowPermanentDelete(context.Context, bool) (engine.Settings, error) {
	l.record("SetAllowPermanentDelete")
	return engine.Settings{}, nil
}

func (l *localRecorder) CopyResumeCommand(context.Context, model.SessionRef) (string, error) {
	l.record("CopyResumeCommand")
	return "claude --resume x", nil
}

func (l *localRecorder) RevealSource(context.Context, model.SessionRef) (string, error) {
	l.record("RevealSource")
	return "/tmp", nil
}

func (l *localRecorder) ListGroups(context.Context, engine.GroupMode, engine.FilterOpts) ([]engine.GroupNode, error) {
	l.record("ListGroups")
	return nil, nil
}

func TestApp_DroppedRemoteNeverFallsBackToLocal(t *testing.T) {
	local := &localRecorder{}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)
	// Was connected to box, then the link dropped.
	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.state = ConnectionState{Phase: ConnReconnecting, Host: "box", Generation: 1, Error: "connection lost"}

	ref := model.SessionRef{Agent: model.AgentCodex, ID: "s1"}
	if _, err := a.DeleteSessions([]model.SessionRef{ref}, "tok"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("DeleteSessions err = %v, want ErrDisconnected", err)
	}
	if _, err := a.SetAllowPermanentDelete(true); !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("SetAllowPermanentDelete err = %v, want ErrDisconnected", err)
	}
	if _, err := a.ListGroups(engine.GroupModeDirAgent, engine.FilterOpts{}); !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("ListGroups err = %v, want ErrDisconnected", err)
	}
	if cmd, err := a.CopyResumeCommand(ref); err == nil {
		t.Errorf("CopyResumeCommand = %q, want an error", cmd)
	}
	if err := a.RevealSource(ref); err == nil || !strings.Contains(err.Error(), "not supported on remote hosts") {
		t.Errorf("RevealSource err = %v", err)
	}
	if len(local.calls) != 0 {
		t.Fatalf("calls reached Local while disconnected from box: %v", local.calls)
	}

	// Disconnect is the only way back to Local.
	a.conn.Disconnect()
	if cmd, err := a.CopyResumeCommand(ref); err != nil || cmd != "claude --resume x" {
		t.Fatalf("after disconnect: %q, %v", cmd, err)
	}
}

func TestConnection_SwitchingHostsIsOfflineUntilConnected(t *testing.T) {
	c := newConnection(nil)
	c.wantHost = "a"
	c.localActive = false // a was connected
	prev := reconnectBackoff
	reconnectBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { reconnectBackoff = prev })
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, errors.New("cancelled")
	}
	if err := c.Connect(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()
	r := c.route()
	if _, ok := r.backend.(offlineBackend); !ok || r.host != "b" {
		t.Fatalf("while connecting a->b route = %+v", r)
	}
}

func TestLocalOnlyEmitter_DropsWhileRemote(t *testing.T) {
	c := newConnection(nil)
	var got []string
	e := localOnlyEmitter(engine.EmitterFunc(func(name string, _ any) { got = append(got, name) }), c)

	e.Emit("catalog:changed", nil)
	c.mu.Lock()
	c.wantHost, c.localActive = "box", false
	c.mu.Unlock()
	e.Emit("index:progress", nil)
	c.Disconnect()
	e.Emit("catalog:changed", nil)

	if strings.Join(got, ",") != "catalog:changed,catalog:changed" {
		t.Fatalf("emitted %v", got)
	}
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

func TestConnection_PermanentDialErrorStopsRetrying(t *testing.T) {
	prev := reconnectBackoff
	reconnectBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { reconnectBackoff = prev })
	for _, failure := range []error{remote.ErrServerBundleNotFound, remote.ErrSSHClientNotFound, remote.ErrSSHAuthentication, remote.ErrSSHHostKey} {
		t.Run(failure.Error(), func(t *testing.T) {
			c := newConnection(nil)
			defer c.Shutdown()
			var mu sync.Mutex
			calls := 0
			c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				return nil, fmt.Errorf("connect prerequisite: %w", failure)
			}
			if err := c.Connect(context.Background(), "box"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && c.snapshot().Phase != ConnDisconnected {
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(50 * time.Millisecond)
			st := c.snapshot()
			if st.Phase != ConnDisconnected || st.Host != "box" || !strings.Contains(st.Error, failure.Error()) {
				t.Fatalf("state = %+v", st)
			}
			mu.Lock()
			got := calls
			mu.Unlock()
			if got != 1 {
				t.Fatalf("dial calls = %d, want 1", got)
			}
		})
	}
}

// Leaving a host on purpose closes its ssh master; a retry of the same host
// keeps it for the next dial.
func TestConnection_ExplicitLeaveStopsMaster(t *testing.T) {
	c := newConnection(nil)
	// Test the multiplexed policy on any OS; Windows defaults are tested separately.
	c.opts.ControlMaster = "auto"
	var mu sync.Mutex
	var stopped []string
	c.stopMaster = func(_ context.Context, alias string, _ remote.SSHOptions) error {
		mu.Lock()
		stopped = append(stopped, alias)
		mu.Unlock()
		return nil
	}
	prev := reconnectBackoff
	reconnectBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { reconnectBackoff = prev })
	c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	got := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(stopped, ",")
	}

	c.Disconnect() // from Local: nothing to stop
	for _, host := range []string{"a", "a", "b"} {
		if err := c.Connect(context.Background(), host); err != nil {
			t.Fatal(err)
		}
	}
	if g := got(); g != "a" {
		t.Fatalf("after a, a (retry), b: stopped %q, want \"a\"", g)
	}
	c.Disconnect()
	if g := got(); g != "a,b" {
		t.Fatalf("after disconnect: stopped %q", g)
	}
	if err := c.Connect(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	c.Shutdown()
	if g := got(); g != "a,b,c" {
		t.Fatalf("after shutdown: stopped %q", g)
	}

	// With multiplexing off there is no master to stop.
	c2 := newConnection(nil)
	c2.opts.ControlMaster = "no"
	c2.dial = c.dial
	c2.stopMaster = c.stopMaster
	if err := c2.Connect(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
	c2.Shutdown()
	if g := got(); g != "a,b,c" {
		t.Fatalf("ControlMaster=no: stopped %q", g)
	}
}
