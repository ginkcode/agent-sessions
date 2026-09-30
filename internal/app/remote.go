package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// Connection phases reported to the frontend. A dropped remote stays on the
// selected host; it does not fall back to Local.
const (
	ConnLocal        = "local"
	ConnConnecting   = "connecting"
	ConnConnected    = "connected"
	ConnDisconnected = "disconnected"
	ConnReconnecting = "reconnecting"
)

// connectionEvent is the Wails event carrying ConnectionState.
const connectionEvent = "connection:state"

// askpassEvent is the Wails event carrying AskpassPrompt.
const askpassEvent = "askpass:prompt"

// AskpassPrompt is the event payload for askpass:prompt.
type AskpassPrompt struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
}

// reconnect delays. Mutations are never retried across these.
var reconnectBackoff = []time.Duration{
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

// ConnectionState is the snapshot the frontend renders.
type ConnectionState struct {
	Phase        string           `json:"phase"`
	Host         string           `json:"host,omitempty"`
	Generation   uint64           `json:"generation"`
	Error        string           `json:"error,omitempty"`
	Capabilities rpc.Capabilities `json:"capabilities"`
	AppVersion   string           `json:"appVersion,omitempty"`
}

// HostEntry is one row in the host picker.
type HostEntry struct {
	Name     string `json:"name"`
	HostName string `json:"hostName,omitempty"`
	User     string `json:"user,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// sessionDialer starts a remote serve session. Tests replace it.
type sessionDialer func(ctx context.Context, alias string, opts remote.SSHOptions, clientEnv map[string]string, emitter engine.Emitter) (*remote.Session, error)

func defaultDialer(ctx context.Context, alias string, opts remote.SSHOptions, clientEnv map[string]string, emitter engine.Emitter) (*remote.Session, error) {
	return remote.StartSession(ctx, alias, opts, clientEnv, emitter)
}

type connSnap struct {
	state   ConnectionState
	session *remote.Session
	gen     uint64
}

// connection tracks the active target. The local engine stays running while a
// remote session is connected; events from a stale generation are dropped.
type connection struct {
	mu      sync.Mutex
	state   ConnectionState
	session *remote.Session
	// client and xport serve the connected session (set with it; tests set
	// them directly).
	client   engine.Backend
	xport    remote.ArtifactTransport
	gen      uint64
	dial     sessionDialer
	opts     remote.SSHOptions
	env      map[string]string
	emitter  engine.Emitter
	askpass  *remote.AskpassBroker
	wantHost string // host the user selected; empty means Local
	// localActive: Local still serves calls. True until a host first
	// connects, and again after Disconnect.
	localActive bool
	attempt     int
	cancel      context.CancelFunc
	stopped     bool
}

func newConnection(emitter engine.Emitter) *connection {
	c := &connection{
		state:       ConnectionState{Phase: ConnLocal},
		dial:        defaultDialer,
		emitter:     emitter,
		localActive: true,
	}
	broker, err := remote.NewAskpassBroker(func(id, prompt string) {
		c.emitAskpass(id, prompt)
	})
	if err == nil {
		c.askpass = broker
		execPath, err := os.Executable()
		if err == nil {
			c.opts.AskpassBinary = execPath
		}
		c.opts.AskpassSock = broker.SocketPath()
		c.opts.AskpassToken = broker.Token()
	}
	return c
}

func (c *connection) snapshot() ConnectionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *connection) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// route is one consistent view of where a binding call goes. Callers take a
// single snapshot per call, so the backend that ran a call and the host its
// command is wrapped for cannot disagree.
type route struct {
	backend   engine.Backend // nil means Local
	host      string         // non-empty: the call belongs to this remote host
	transport remote.ArtifactTransport
}

// route returns the live remote while connected. Otherwise Local stays
// active only during a first connect from Local; once a host has been in
// use, a dropped or switching connection gets the offline backend, never
// Local.
func (c *connection) route() route {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Phase == ConnConnected && c.client != nil {
		return route{backend: c.client, host: c.state.Host, transport: c.xport}
	}
	if c.wantHost != "" && !c.localActive {
		return route{backend: offlineBackend{host: c.wantHost}, host: c.wantHost}
	}
	return route{}
}

// wrap turns a command built on the route's host into one the user runs
// locally: `ssh -t <host> '<cmd>'` for a remote, unchanged for Local.
func (r route) wrap(cmd string) string {
	if r.host == "" {
		return cmd
	}
	return remote.WrapSSHCommand(r.host, cmd)
}

func (c *connection) backend(local engine.Backend) engine.Backend {
	if b := c.route().backend; b != nil {
		return b
	}
	return local
}

func (c *connection) setEmitter(e engine.Emitter) {
	c.mu.Lock()
	c.emitter = e
	c.mu.Unlock()
}

func (c *connection) emit() {
	c.mu.Lock()
	e := c.emitter
	st := c.state
	c.mu.Unlock()
	if e != nil {
		e.Emit(connectionEvent, st)
	}
}

func (c *connection) emitAskpass(id, prompt string) {
	c.mu.Lock()
	e := c.emitter
	c.mu.Unlock()
	if e != nil {
		e.Emit(askpassEvent, AskpassPrompt{ID: id, Prompt: prompt})
	}
}

func (c *connection) askpassReply(id, answer string) bool {
	c.mu.Lock()
	broker := c.askpass
	c.mu.Unlock()
	if broker == nil {
		return false
	}
	return broker.Reply(id, answer)
}

func (c *connection) setEnv(env map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.env = env
}

func (c *connection) getEnv() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.env == nil {
		return nil
	}
	cp := make(map[string]string, len(c.env))
	for k, v := range c.env {
		cp[k] = v
	}
	return cp
}

// setPhase publishes state. gen, when non-zero, must still be current or the
// update is dropped (a newer connect/disconnect won).
func (c *connection) setPhase(gen uint64, phase, host, errMsg string, caps rpc.Capabilities, appVer string) bool {
	c.mu.Lock()
	if gen != 0 && gen != c.gen {
		c.mu.Unlock()
		return false
	}
	c.state = ConnectionState{
		Phase:        phase,
		Host:         host,
		Generation:   c.gen,
		Error:        errMsg,
		Capabilities: caps,
		AppVersion:   appVer,
	}
	c.mu.Unlock()
	c.emit()
	return true
}

// Connect begins a connection to alias. Coming from Local, Local stays the
// active backend until the remote handshake finishes; coming from another
// host, calls fail as disconnected meanwhile. A newer Connect or Disconnect
// cancels this one.
func (c *connection) Connect(ctx context.Context, alias string) error {
	if err := remote.ValidateHostAlias(alias); err != nil {
		return err
	}
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return errors.New("app is shut down")
	}
	c.closeLocked()
	c.gen++
	gen := c.gen
	c.wantHost = alias
	c.attempt = 0
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	dial := c.dial
	opts := c.opts
	env := c.env
	emitter := c.emitter
	c.state = ConnectionState{Phase: ConnConnecting, Host: alias, Generation: gen}
	c.mu.Unlock()
	c.emit()

	go c.dialLoop(runCtx, gen, alias, dial, opts, env, emitter)
	return nil
}

func (c *connection) dialLoop(ctx context.Context, gen uint64, alias string, dial sessionDialer, opts remote.SSHOptions, env map[string]string, emitter engine.Emitter) {
	for {
		if ctx.Err() != nil {
			return
		}
		sess, err := dial(ctx, alias, opts, env, generationEmitter(emitter, gen, c))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.mu.Lock()
			still := c.gen == gen && c.wantHost == alias
			c.attempt++
			attempt := c.attempt
			c.mu.Unlock()
			if !still {
				return
			}
			c.setPhase(gen, ConnDisconnected, alias, err.Error(), rpc.Capabilities{}, "")
			if permanentDialError(err) {
				// Stay disconnected on the host; the user can Retry.
				return
			}
			delay := reconnectBackoff[(attempt-1)%len(reconnectBackoff)]
			c.setPhase(gen, ConnReconnecting, alias, err.Error(), rpc.Capabilities{}, "")
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}

		c.mu.Lock()
		if c.gen != gen || c.wantHost != alias {
			c.mu.Unlock()
			_ = sess.Close()
			return
		}
		c.session = sess
		if cl := sess.Client(); cl != nil {
			c.client = cl
		}
		c.xport = sess
		c.localActive = false
		c.attempt = 0
		caps := rpc.Capabilities{}
		appVer := ""
		if res := sess.InitializeResult(); res != nil {
			caps = res.Capabilities
			appVer = res.AppVersion
		}
		c.state = ConnectionState{
			Phase:        ConnConnected,
			Host:         alias,
			Generation:   gen,
			Capabilities: caps,
			AppVersion:   appVer,
		}
		c.mu.Unlock()
		c.emit()

		select {
		case <-ctx.Done():
			_ = sess.Close()
			return
		case <-sess.Done():
		}
		_ = sess.Close()

		c.mu.Lock()
		if c.gen != gen || c.wantHost != alias {
			c.mu.Unlock()
			return
		}
		c.session, c.client, c.xport = nil, nil, nil
		c.attempt++
		attempt := c.attempt
		c.mu.Unlock()
		// Dropped, not a user disconnect: stay on the host and retry.
		c.setPhase(gen, ConnDisconnected, alias, "connection lost", rpc.Capabilities{}, "")
		delay := reconnectBackoff[(attempt-1)%len(reconnectBackoff)]
		c.setPhase(gen, ConnReconnecting, alias, "connection lost", rpc.Capabilities{}, "")
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// permanentDialError reports failures that a reconnect cannot fix.
func permanentDialError(err error) bool {
	return errors.Is(err, remote.ErrServerBundleNotFound) ||
		errors.Is(err, remote.ErrServerVersionMismatch) ||
		errors.Is(err, remote.ErrUnsupportedOS) ||
		errors.Is(err, remote.ErrUnsupportedArch) ||
		errors.Is(err, remote.ErrInvalidHostAlias) ||
		errors.Is(err, rpc.ErrProtocolMismatch)
}

// Disconnect returns to Local and stops reconnects. The selected host is cleared.
func (c *connection) Disconnect() {
	c.mu.Lock()
	c.wantHost = ""
	c.localActive = true
	c.closeLocked()
	c.gen++
	c.state = ConnectionState{Phase: ConnLocal, Generation: c.gen}
	c.mu.Unlock()
	c.emit()
}

// Shutdown cancels any dial and closes the session. Called from App.Close.
func (c *connection) Shutdown() {
	c.mu.Lock()
	c.stopped = true
	c.wantHost = ""
	c.closeLocked()
	broker := c.askpass
	c.askpass = nil
	c.mu.Unlock()
	if broker != nil {
		_ = broker.Close()
	}
}

func (c *connection) closeLocked() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
	c.client, c.xport = nil, nil
}

// localOnlyEmitter forwards local engine events only while Local serves the
// UI, so local catalog or index changes never land in a remote host's view.
// Switching back to Local reloads everything, so nothing is lost.
func localOnlyEmitter(inner engine.Emitter, c *connection) engine.Emitter {
	return engine.EmitterFunc(func(name string, payload any) {
		if c.route().backend != nil {
			return
		}
		inner.Emit(name, payload)
	})
}

// generationEmitter drops events whose connection generation is no longer current.
func generationEmitter(inner engine.Emitter, gen uint64, c *connection) engine.Emitter {
	return engine.EmitterFunc(func(name string, payload any) {
		if c.generation() != gen {
			return
		}
		if inner != nil {
			inner.Emit(name, payload)
		}
	})
}
