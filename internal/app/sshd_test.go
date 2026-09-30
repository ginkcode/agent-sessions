package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/remote/sshtest"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// These drive the real connection loop (probe, deploy, serve) against a
// Docker sshd; they are skipped unless AGENT_SESSIONS_SSH_DOCKER=1. Run them
// with `make test-ssh`.

// phaseLog records every ConnectionState the connection emits.
type phaseLog struct {
	mu     sync.Mutex
	states []ConnectionState
}

func (l *phaseLog) Emit(name string, payload any) {
	if name != connectionEvent {
		return
	}
	l.mu.Lock()
	l.states = append(l.states, payload.(ConnectionState))
	l.mu.Unlock()
}

// waitFor returns the first state from index from on that matches ok.
func (l *phaseLog) waitFor(t *testing.T, from int, timeout time.Duration, ok func(ConnectionState) bool) (int, ConnectionState) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for i := from; i < len(l.states); i++ {
			if ok(l.states[i]) {
				st := l.states[i]
				l.mu.Unlock()
				return i, st
			}
		}
		l.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Fatalf("no matching state within %v; saw %+v", timeout, l.states)
	return 0, ConnectionState{}
}

func sshdConnection(t *testing.T, h *sshtest.Host) (*connection, *phaseLog) {
	t.Helper()
	prev := reconnectBackoff
	reconnectBackoff = []time.Duration{200 * time.Millisecond}
	t.Cleanup(func() { reconnectBackoff = prev })

	log := &phaseLog{}
	c := newConnection(log)
	t.Cleanup(c.Shutdown)
	c.opts.ConfigFile = h.ConfigFile
	c.opts.ControlPath = h.ControlPath
	return c, log
}

func phaseIs(phase string) func(ConnectionState) bool {
	return func(st ConnectionState) bool { return st.Phase == phase }
}

func TestSSHD_ConnectionReconnectsAfterDrop(t *testing.T) {
	h := sshtest.Start(t)
	t.Setenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR", sshtest.ServerBundleDir(t, version.Current()))
	c, log := sshdConnection(t, h)
	alias := h.Alias(sshtest.UserZsh)

	if err := c.Connect(context.Background(), alias); err != nil {
		t.Fatal(err)
	}
	i, st := log.waitFor(t, 0, 60*time.Second, func(st ConnectionState) bool {
		return st.Phase == ConnConnected || st.Phase == ConnDisconnected
	})
	if st.Phase != ConnConnected || st.Host != alias || st.AppVersion != version.Current() {
		t.Fatalf("first connect: %+v", st)
	}
	backend := c.backend(nil)
	if backend == nil {
		t.Fatal("connected without a remote backend")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := backend.Scan(ctx); err != nil {
		t.Fatalf("remote scan: %v", err)
	}

	// Kill the remote server: the loop reports the drop, stays on the host,
	// and reconnects to a new session on its own.
	h.Exec(t, `pkill -f 'agent-sessions-cli serve'`)
	i, st = log.waitFor(t, i+1, 15*time.Second, phaseIs(ConnDisconnected))
	if st.Host != alias || st.Error != "connection lost" {
		t.Fatalf("drop state: %+v", st)
	}
	i, _ = log.waitFor(t, i+1, 5*time.Second, phaseIs(ConnReconnecting))
	_, st = log.waitFor(t, i+1, 60*time.Second, phaseIs(ConnConnected))
	if st.Host != alias {
		t.Fatalf("reconnected to %q", st.Host)
	}
	again := c.backend(nil)
	if again == nil || again == backend {
		t.Fatal("reconnect did not install a new session")
	}
	if _, err := again.AgentCounts(ctx, engine.FilterOpts{}); err != nil {
		t.Fatalf("call after reconnect: %v", err)
	}

	c.Disconnect()
	if st := c.snapshot(); st.Phase != ConnLocal || st.Host != "" {
		t.Fatalf("after disconnect: %+v", st)
	}
}

func TestSSHD_VersionMismatchStopsRetrying(t *testing.T) {
	h := sshtest.Start(t)
	// A server stamped with another version deploys, then fails the version
	// check. Redeploying the same bundle cannot help, so the loop must stop.
	t.Setenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR", sshtest.ServerBundleDir(t, "0.0.0-mismatch"))
	c, log := sshdConnection(t, h)
	alias := h.Alias(sshtest.UserSh)

	var mu sync.Mutex
	dials := 0
	c.dial = func(ctx context.Context, alias string, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) (*remote.Session, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return remote.StartSession(ctx, alias, opts, env, emitter)
	}
	if err := c.Connect(context.Background(), alias); err != nil {
		t.Fatal(err)
	}
	_, st := log.waitFor(t, 0, 60*time.Second, phaseIs(ConnDisconnected))
	if !strings.Contains(st.Error, "server version mismatch") {
		t.Fatalf("state: %+v", st)
	}
	time.Sleep(time.Second) // several backoff periods
	mu.Lock()
	defer mu.Unlock()
	if dials != 1 {
		t.Fatalf("dial attempts = %d, want 1", dials)
	}
	if got := c.snapshot(); got.Phase != ConnDisconnected {
		t.Fatalf("phase after wait = %q", got.Phase)
	}
}
