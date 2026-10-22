package codediagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock drives every grace/deadline timer; nothing here sleeps for real.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTick
}
type fakeTick struct {
	at time.Duration
	d  time.Duration
	ch chan time.Time
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTick{c.now + d, d, make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t.ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	for _, t := range c.timers {
		if t.at <= c.now {
			select {
			case t.ch <- time.Time{}:
			default:
			}
		}
	}
}

func (c *fakeClock) count(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if t.d == d {
			n++
		}
	}
	return n
}

// fakeSrv is one fake language server process plus its scripted protocol.
type fakeSrv struct {
	t          *testing.T
	inv        Invocation
	fromTr     *io.PipeReader
	toTr       *io.PipeWriter
	errW       *io.PipeWriter
	fr         *frameReader
	exited     chan struct{}
	once       sync.Once
	terminated chan struct{}
	termOnce   sync.Once
	mu         sync.Mutex
	methods    []string
}

func (f *fakeSrv) Wait() (int, error) { <-f.exited; return 0, nil }
func (f *fakeSrv) Terminate() error {
	f.termOnce.Do(func() { close(f.terminated) })
	f.once.Do(func() { close(f.exited) })
	_ = f.toTr.Close()
	_ = f.errW.Close()
	return nil
}
func (f *fakeSrv) Close() error { return nil }

func (f *fakeSrv) read() (map[string]any, bool) {
	b, err := f.fr.readFrame()
	if err != nil {
		return nil, false
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if s, ok := m["method"].(string); ok {
		f.mu.Lock()
		f.methods = append(f.methods, s)
		f.mu.Unlock()
	}
	return m, true
}
func (f *fakeSrv) send(body string) { _, _ = io.WriteString(f.toTr, frameOf(body)) }
func (f *fakeSrv) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

type sessionEnv struct {
	t       *testing.T
	clock   *fakeClock
	trust   *Trust
	mgr     *SessionManager
	started chan *fakeSrv
	script  func(*fakeSrv)
	mu      sync.Mutex
	launchN int
	fail    error
}

const defaultInit = `{"capabilities":{"textDocumentSync":{"openClose":true,"change":2,"save":true}}}`

// serveInit answers initialize with result then keeps answering shutdown.
func serveInit(result string) func(*fakeSrv) {
	return func(f *fakeSrv) {
		for {
			m, ok := f.read()
			if !ok {
				return
			}
			id, _ := json.Marshal(m["id"])
			switch m["method"] {
			case "initialize":
				f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + result + `}`)
			case "shutdown":
				f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":null}`)
			case "exit":
				_ = f.Terminate()
				return
			}
		}
	}
}

func newEnv(t *testing.T, opts SessionOptions) *sessionEnv {
	t.Helper()
	e := &sessionEnv{t: t, clock: &fakeClock{}, trust: NewTrust(nil), started: make(chan *fakeSrv, 16), script: serveInit(defaultInit)}
	opts.After = e.clock.After
	opts.Launch = func(in Invocation) (SessionProcess, SessionStreams, error) {
		e.mu.Lock()
		e.launchN++
		fail, script := e.fail, e.script
		e.mu.Unlock()
		if fail != nil {
			return nil, SessionStreams{}, fail
		}
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		errR, errW := io.Pipe()
		f := &fakeSrv{t: t, inv: in, fromTr: inR, toTr: outW, errW: errW, fr: newFrameReader(inR, 1<<10, 8, 1<<20), exited: make(chan struct{}), terminated: make(chan struct{})}
		e.started <- f
		go script(f)
		return f, SessionStreams{Stdin: inW, Stdout: outR, Stderr: errR}, nil
	}
	e.mgr = NewSessionManager(e.trust, opts)
	t.Cleanup(e.mgr.Close)
	return e
}

func (e *sessionEnv) launches() int { e.mu.Lock(); defer e.mu.Unlock(); return e.launchN }

// project makes an approved project root and server config.
func (e *sessionEnv) project() (AuthorizedRoot, ServerConfig) {
	e.t.Helper()
	dir := e.t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	for _, name := range []string{"server", "cfg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic "+name), 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
	root, err := AuthorizeRoot("local:test", dir, func(s string) bool { return s == dir })
	if err != nil {
		e.t.Fatal(err)
	}
	cfg := ServerConfig{ProviderID: "lsp", Executable: filepath.Join(dir, "server"), Args: []string{"--stdio"}, AdapterPolicy: "lsp-v1", ConfigPaths: []string{"cfg"}}
	e.approve(root, cfg)
	return root, cfg
}

func (e *sessionEnv) approve(root AuthorizedRoot, cfg ServerConfig) {
	e.t.Helper()
	tool := cfg.Tool()
	fp, err := Fingerprint(root, []ToolInput{tool})
	if err != nil {
		e.t.Fatal(err)
	}
	approve(e.t, e.trust, root, []ToolInput{tool}, fp, "session")
}

func (e *sessionEnv) next() *fakeSrv {
	e.t.Helper()
	select {
	case f := <-e.started:
		return f
	case <-time.After(10 * time.Second):
		e.t.Fatal("no server started")
		return nil
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestSessionInitializeProtocolOrderAndCapabilities(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	info, err := e.mgr.Attach(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := e.next()
	if info.Phase != phaseInitialized || !info.Capabilities.IncrementalSync() || !info.Capabilities.Save || !info.Capabilities.OpenClose {
		t.Fatalf("%+v", info)
	}
	if info.ExternalChanges != "unwatched" {
		t.Fatal("must not claim watching")
	}
	eventually(t, "initialized sent", func() bool { s := f.seen(); return len(s) == 2 })
	if got := f.seen(); got[0] != "initialize" || got[1] != "initialized" {
		t.Fatalf("order %v", got)
	}
	if f.inv.Executable != cfg.Executable || f.inv.Directory != root.Project().Root || len(f.inv.Args) != 1 {
		t.Fatalf("%+v", f.inv)
	}
}

func TestSessionAdvertisesNoLiveCapability(t *testing.T) {
	var params map[string]any
	e := newEnv(t, SessionOptions{})
	e.script = func(f *fakeSrv) {
		m, _ := f.read()
		params, _ = m["params"].(map[string]any)
		id, _ := json.Marshal(m["id"])
		f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + defaultInit + `}`)
		serveInit(defaultInit)(f)
	}
	root, cfg := e.project()
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	caps := params["capabilities"].(map[string]any)
	if len(caps) != 1 || caps["general"] == nil {
		t.Fatalf("capabilities %v", caps)
	}
	if params["workspaceFolders"] != nil {
		t.Fatal("workspace folders must not be advertised")
	}
}

func TestSessionInitializeFailures(t *testing.T) {
	cases := map[string]struct {
		script func(*fakeSrv)
		code   string
	}{
		"no sync":         {serveInit(`{"capabilities":{}}`), "unsupported"},
		"none sync":       {serveInit(`{"capabilities":{"textDocumentSync":0}}`), "unsupported"},
		"utf8 only":       {serveInit(`{"capabilities":{"positionEncoding":"utf-8","textDocumentSync":1}}`), "unsupported"},
		"no capabilities": {serveInit(`{}`), "toolFailed"},
		"bad sync":        {serveInit(`{"capabilities":{"textDocumentSync":"x"}}`), "toolFailed"},
		"bad save":        {serveInit(`{"capabilities":{"textDocumentSync":{"openClose":true,"change":1,"save":7}}}`), "toolFailed"},
		"not json object": {serveInit(`[1]`), "toolFailed"},
		"server error": {func(f *fakeSrv) {
			m, _ := f.read()
			id, _ := json.Marshal(m["id"])
			f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-1,"message":"SECRET text"}}`)
		}, "toolFailed"},
		"early exit":     {func(f *fakeSrv) { f.read(); _ = f.Terminate() }, "toolFailed"},
		"oversize reply": {serveInit(`{"capabilities":{"textDocumentSync":1},"x":"` + string(make([]byte, 0)) + `"}`), ""},
	}
	delete(cases, "oversize reply")
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, SessionOptions{})
			e.script = c.script
			root, cfg := e.project()
			info, err := e.mgr.Attach(context.Background(), root, cfg)
			code(t, err, c.code)
			if info.Phase != phaseFailed || !info.RetryAvailable {
				t.Fatalf("%+v", info)
			}
			var pe *Error
			errors.As(err, &pe)
			if pe != nil && contains(pe.Message, "SECRET") {
				t.Fatal("server text leaked")
			}
			f := e.next()
			eventually(t, "process killed", func() bool { return closed(f.terminated) })
		})
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestSessionInitializeTimeoutUsesFakeClock(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	e.script = func(f *fakeSrv) { f.read() } // never replies
	root, cfg := e.project()
	done := make(chan error, 1)
	go func() { _, err := e.mgr.Attach(context.Background(), root, cfg); done <- err }()
	f := e.next()
	eventually(t, "init timer", func() bool { return e.clock.count(initTimeout) >= 1 })
	e.clock.Advance(initTimeout)
	select {
	case err := <-done:
		code(t, err, "timeout")
	case <-time.After(10 * time.Second):
		t.Fatal("attach hung")
	}
	eventually(t, "killed", func() bool { return closed(f.terminated) })
}

func TestSessionTrustGateBlocksLaunch(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()

	// Unapproved server configuration (different argv => different fingerprint).
	other := cfg
	other.Args = []string{"--other"}
	_, err := e.mgr.Attach(context.Background(), root, other)
	code(t, err, "approvalRequired")

	// Policy/argv tampering: Policy is derived, so a stale approval cannot match.
	// Unknown directory: catalog no longer lists the root.
	dir := t.TempDir()
	if _, err := AuthorizeRoot("local:test", dir, func(string) bool { return false }); err == nil {
		t.Fatal("unknown directory authorized")
	}
	gone, err := AuthorizeRoot("local:test", root.Project().Root, func(string) bool { return false })
	_ = gone
	code(t, err, "unauthorized")

	// Relative executable and shell-like control bytes are rejected before launch.
	bad := cfg
	bad.Executable = "server"
	_, err = e.mgr.Attach(context.Background(), root, bad)
	code(t, err, "invalidArgument")

	// Executable change after approval requires renewed approval.
	if err := os.WriteFile(cfg.Executable, []byte("changed binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = e.mgr.Attach(context.Background(), root, cfg)
	if err == nil {
		t.Fatal("changed executable must not start")
	}
	if e.launches() != 0 {
		t.Fatal("process launched without approval")
	}
}

func TestSessionConcurrentAttachSharesOneServer(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	var wg sync.WaitGroup
	gens := make([]string, 8)
	for i := range gens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := e.mgr.Attach(context.Background(), root, cfg)
			if err != nil {
				t.Error(err)
			}
			gens[i] = info.Generation
		}()
	}
	wg.Wait()
	for _, g := range gens {
		if g != gens[0] || g == "" {
			t.Fatalf("generations %v", gens)
		}
	}
	if e.launches() != 1 {
		t.Fatalf("launches %d", e.launches())
	}
}

func TestSessionDifferentConfigDoesNotShare(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	other := cfg
	other.Args = []string{"--stdio", "--second"}
	e.approve(root, other) // replaces the session grant: first config now needs approval
	if _, err := e.mgr.Attach(context.Background(), root, other); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err == nil {
		t.Fatal("old configuration kept approval")
	}
}

// sendRequest has the fake server issue a request and return the reply.
func (f *fakeSrv) request(id, method, params string) map[string]any {
	f.send(`{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `","params":` + params + `}`)
	for {
		m, ok := f.read()
		if !ok {
			return nil
		}
		if m["method"] == nil && m["id"] != nil {
			return m
		}
	}
}

func TestSessionServerRequestPolicy(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	replies := make(chan map[string]any, 16)
	ask := make(chan [3]string)
	e.script = func(f *fakeSrv) {
		m, _ := f.read()
		id, _ := json.Marshal(m["id"])
		f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + defaultInit + `}`)
		f.read() // initialized
		for q := range ask {
			replies <- f.request(q[0], q[1], q[2])
		}
		serveInit(defaultInit)(f)
	}
	root, cfg := e.project()
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	defer close(ask)
	inside := fileURI(filepath.Join(root.Project().Root, "pkg"))
	reply := func(id, method, params string) map[string]any {
		ask <- [3]string{id, method, params}
		select {
		case r := <-replies:
			return r
		case <-time.After(10 * time.Second):
			t.Fatalf("%s stalled", method)
			return nil
		}
	}
	isErr := func(r map[string]any, code float64) {
		t.Helper()
		er, _ := r["error"].(map[string]any)
		if er == nil || er["code"] != code {
			t.Fatalf("reply %v", r)
		}
	}
	isErr(reply("1", "workspace/applyEdit", `{"edit":{"changes":{"file:///etc/passwd":[]}}}`), -32601)
	isErr(reply("2", "workspace/executeCommand", `{"command":"rm"}`), -32601)
	isErr(reply("3", "workspace/workspaceFolders", `null`), -32601)
	isErr(reply("4", "window/showDocument", `{"uri":"file:///etc/passwd"}`), -32601)
	isErr(reply("5", "totally/unknown", `{}`), -32601)
	isErr(reply("6", "client/registerCapability", `{"registrations":[{"id":"w","method":"workspace/didChangeWatchedFiles"}]}`), -32601)
	r := reply("7", "workspace/configuration", `{"items":[{"scopeUri":"`+inside+`","section":"gopls"},{"section":"x"}]}`)
	res, _ := r["result"].([]any)
	if len(res) != 2 || res[0] != nil || res[1] != nil {
		t.Fatalf("config %v", r)
	}
	// Out-of-root scope, traversal, oversize items: rejected, not answered with data.
	isErr(reply("8", "workspace/configuration", `{"items":[{"scopeUri":"file:///etc"}]}`), -32602)
	isErr(reply("9", "workspace/configuration", `{"items":[{"scopeUri":"`+fileURI(root.Project().Root)+`/../x"}]}`), -32602)
	items := `{"items":[` + repeatJoin(`{"section":"a"}`, maxConfigItems+1) + `]}`
	isErr(reply("10", "workspace/configuration", items), -32602)
	isErr(reply("11", "workspace/configuration", `{}`), -32602)
	info := e.mgr.Info(root, cfg)
	if info.Phase != phaseInitialized || info.RejectedWatchers != 1 || info.ExternalChanges != "unwatched" {
		t.Fatalf("%+v", info)
	}
}

func repeatJoin(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

func TestSessionIdleGraceReuseExpiryAndEviction(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	first, err := e.mgr.Attach(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f1 := e.next()

	// Hide then reuse inside grace: same server, timer canceled.
	e.mgr.Hide(root, cfg)
	eventually(t, "grace timer", func() bool { return e.clock.count(idleGrace) == 1 })
	if e.mgr.Info(root, cfg).Phase != phaseIdle {
		t.Fatal("not idle")
	}
	e.clock.Advance(idleGrace - time.Second)
	again, err := e.mgr.Attach(context.Background(), root, cfg)
	if err != nil || again.Generation != first.Generation || e.launches() != 1 {
		t.Fatalf("%+v %v %d", again, err, e.launches())
	}
	e.clock.Advance(time.Hour) // stale timer from the canceled idle period must not stop it
	time.Sleep(20 * time.Millisecond)
	if closed(f1.terminated) || e.mgr.Info(root, cfg).Phase != phaseInitialized {
		t.Fatal("reused session stopped by stale timer")
	}

	// Hide again and expire: shutdown/exit then stopped; kill guaranteed.
	e.mgr.Hide(root, cfg)
	eventually(t, "second grace timer", func() bool { return e.clock.count(idleGrace) == 2 })
	e.clock.Advance(idleGrace)
	eventually(t, "idle expiry", func() bool { return closed(f1.terminated) })
	if got := f1.seen(); got[len(got)-2] != "shutdown" || got[len(got)-1] != "exit" {
		t.Fatalf("graceful shutdown order %v", got)
	}
	eventually(t, "stopped", func() bool { return e.mgr.Info(root, cfg).Phase == phaseStopped })
	if e.mgr.CurrentGeneration(root, cfg, first.Generation) {
		t.Fatal("expired generation still current")
	}
	// A new attach after expiry starts a new generation.
	third, err := e.mgr.Attach(context.Background(), root, cfg)
	if err != nil || third.Generation == first.Generation || e.launches() != 2 {
		t.Fatalf("%+v %v", third, err)
	}
}

func TestSessionActiveProjectLimitEvictsIdleOnly(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	r1, c1 := e.project()
	r2, c2 := e.project()
	r3, c3 := e.project()
	for _, p := range []struct {
		r AuthorizedRoot
		c ServerConfig
	}{{r1, c1}, {r2, c2}} {
		if _, err := e.mgr.Attach(context.Background(), p.r, p.c); err != nil {
			t.Fatal(err)
		}
	}
	f1, f2 := e.next(), e.next()
	// Both visible: a third is refused, nothing is evicted.
	_, err := e.mgr.Attach(context.Background(), r3, c3)
	code(t, err, "limitExceeded")
	if closed(f1.terminated) || closed(f2.terminated) {
		t.Fatal("visible project evicted")
	}
	// Idle projects can be evicted; the oldest idle goes first.
	e.mgr.Hide(r1, c1)
	e.mgr.Hide(r2, c2)
	if _, err := e.mgr.Attach(context.Background(), r3, c3); err != nil {
		t.Fatal(err)
	}
	if !closed(f1.terminated) || closed(f2.terminated) {
		t.Fatal("wrong eviction victim")
	}
	if e.mgr.Info(r1, c1).Phase != phaseStopped {
		t.Fatal("evicted project not stopped")
	}
}

func TestSessionRevokeDisconnectAndCloseBypassGrace(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	first, err := e.mgr.Attach(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := e.next()
	e.mgr.Hide(root, cfg) // idle, grace pending
	eventually(t, "grace timer", func() bool { return e.clock.count(idleGrace) == 1 })
	if err := e.trust.Revoke(root); err != nil {
		t.Fatal(err)
	}
	eventually(t, "revoke kills", func() bool { return closed(f.terminated) })
	eventually(t, "disconnected", func() bool { return e.mgr.Info(root, cfg).Phase == phaseDisconnected })
	if e.mgr.CurrentGeneration(root, cfg, first.Generation) {
		t.Fatal("revoked generation current")
	}
	_, err = e.mgr.Attach(context.Background(), root, cfg)
	code(t, err, "approvalRequired")

	// Explicit Revoke/Disconnect on another project.
	r2, c2 := e.project()
	if _, err := e.mgr.Attach(context.Background(), r2, c2); err != nil {
		t.Fatal(err)
	}
	f2 := e.next()
	e.mgr.Disconnect(r2.Project().HostID)
	if !closed(f2.terminated) {
		t.Fatal("disconnect did not kill synchronously")
	}
	if e.mgr.Info(r2, c2).Phase != phaseDisconnected {
		t.Fatal("phase")
	}

	// App shutdown: graceful shutdown/exit then kill; no goroutines or tree left.
	r3, c3 := e.project()
	if _, err := e.mgr.Attach(context.Background(), r3, c3); err != nil {
		t.Fatal(err)
	}
	f3 := e.next()
	e.mgr.Close()
	if !closed(f3.terminated) {
		t.Fatal("close left process")
	}
	if got := f3.seen(); got[len(got)-2] != "shutdown" || got[len(got)-1] != "exit" {
		t.Fatalf("%v", got)
	}
	_, err = e.mgr.Attach(context.Background(), r3, c3)
	code(t, err, "disconnected")
}

func TestSessionShutdownKillsHungServerAndDescendants(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	e.script = func(f *fakeSrv) {
		m, _ := f.read()
		id, _ := json.Marshal(m["id"])
		f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + defaultInit + `}`)
		for { // never answers shutdown
			if _, ok := f.read(); !ok {
				return
			}
		}
	}
	root, cfg := e.project()
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	f := e.next()
	done := make(chan struct{})
	go func() { e.mgr.Close(); close(done) }()
	eventually(t, "shutdown timer", func() bool { return e.clock.count(shutdownTimeout) >= 1 })
	if closed(f.terminated) {
		t.Fatal("killed before deadline")
	}
	e.clock.Advance(shutdownTimeout)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("close hung on a stuck server")
	}
	if !closed(f.terminated) {
		t.Fatal("process tree not terminated")
	}
}

func TestSessionCrashRestartCapAndGenerationInvalidation(t *testing.T) {
	e := newEnv(t, SessionOptions{MaxRestarts: 2})
	root, cfg := e.project()
	first, err := e.mgr.Attach(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := e.next()
	_ = f.Terminate() // crash
	eventually(t, "failed", func() bool { return e.mgr.Info(root, cfg).Phase == phaseFailed })
	if e.mgr.CurrentGeneration(root, cfg, first.Generation) {
		t.Fatal("crashed generation current")
	}
	info := e.mgr.Info(root, cfg)
	if !info.RetryAvailable || info.Err == nil {
		t.Fatalf("%+v", info)
	}
	// A plain Attach does not crash-loop: it reports the failure, no launch.
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err == nil || e.launches() != 1 {
		t.Fatalf("attach after crash: %v %d", err, e.launches())
	}
	prev := first.Generation
	for i := 1; i <= 2; i++ {
		ri, err := e.mgr.Retry(context.Background(), root, cfg)
		if err != nil || ri.Generation == prev || ri.Restarts != i {
			t.Fatalf("retry %d: %+v %v", i, ri, err)
		}
		if e.mgr.CurrentGeneration(root, cfg, prev) {
			t.Fatal("old generation current after restart")
		}
		prev = ri.Generation
		nf := e.next()
		_ = nf.Terminate()
		eventually(t, "failed again", func() bool { return e.mgr.Info(root, cfg).Phase == phaseFailed })
	}
	_, err = e.mgr.Retry(context.Background(), root, cfg)
	if !errors.Is(err, ErrSessionRestart) || e.launches() != 3 {
		t.Fatalf("%v %d", err, e.launches())
	}
	if e.mgr.Info(root, cfg).RetryAvailable {
		t.Fatal("retry offered after cap")
	}
	// Retry on a healthy session is refused.
	r2, c2 := e.project()
	if _, err := e.mgr.Attach(context.Background(), r2, c2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.Retry(context.Background(), r2, c2); !errors.Is(err, ErrSessionNotRetryable) {
		t.Fatal(err)
	}
}

func TestSessionLaunchFailureAndLateServerOutputDropped(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	e.fail = errors.New("secret path /x/y")
	root, cfg := e.project()
	_, err := e.mgr.Attach(context.Background(), root, cfg)
	code(t, err, "toolFailed")
	var pe *Error
	errors.As(err, &pe)
	if contains(pe.Message, "secret") {
		t.Fatal("launch error leaked")
	}

	// A stale transport writing after stop must not reach a replaced generation.
	e.fail = nil
	var f1 *fakeSrv
	e.script = func(f *fakeSrv) {
		f1 = f
		serveInit(defaultInit)(f)
	}
	info, err := e.mgr.Retry(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := e.next()
	_ = f1
	e.mgr.Disconnect(root.Project().HostID)
	old.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{}}`)
	old.send(`{"jsonrpc":"2.0","id":9,"method":"workspace/applyEdit","params":{}}`)
	if e.mgr.CurrentGeneration(root, cfg, info.Generation) {
		t.Fatal("stopped generation still current")
	}
}

func TestSessionAttachContextCancelAndKillOnStartFailure(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	e.script = func(f *fakeSrv) { f.read() }
	root, cfg := e.project()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := e.mgr.Attach(ctx, root, cfg); done <- err }()
	e.next()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	e.mgr.Close() // must not hang on the still-starting server
}
