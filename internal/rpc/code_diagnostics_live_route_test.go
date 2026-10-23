package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// R14 full-route fixture. Real: rpc.Client, JSON-RPC framing over pipes,
// rpc.Server, engine.Engine (injected live flag), diagnosticsOwner event pump,
// LiveDocuments, SessionManager, Content-Length LSP transport, gopls/TS bridges,
// Manager and grant store. Fake: only the two language-server processes (framed
// stdio scripts) and timers. The live flag is enabled solely by the explicit
// injection option; production never sets it.

type routeServer struct {
	t      *testing.T
	in     *bufio.Reader
	out    *io.PipeWriter
	errW   *io.PipeWriter
	exited chan struct{}
	once   sync.Once
	mu     sync.Mutex
	events []string
	texts  map[string]string
}

func (f *routeServer) Wait() (int, error) { <-f.exited; return 0, nil }
func (f *routeServer) Terminate() error {
	f.once.Do(func() { close(f.exited) })
	_ = f.out.Close()
	_ = f.errW.Close()
	return nil
}
func (f *routeServer) Close() error { return nil }

func (f *routeServer) send(body string) {
	_, _ = io.WriteString(f.out, fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body))
}

func (f *routeServer) readFrame() (map[string]any, bool) {
	length := -1
	for {
		line, err := f.in.ReadString('\n')
		if err != nil {
			return nil, false
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			length, _ = strconv.Atoi(v)
		}
	}
	if length < 0 {
		return nil, false
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(f.in, body); err != nil {
		return nil, false
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return m, true
}

func (f *routeServer) serve() {
	for {
		m, ok := f.readFrame()
		if !ok {
			return
		}
		method, _ := m["method"].(string)
		id, _ := json.Marshal(m["id"])
		switch method {
		case "initialize":
			f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"capabilities":{"textDocumentSync":{"openClose":true,"change":2,"save":true}}}}`)
		case "shutdown":
			f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":null}`)
		case "exit":
			_ = f.Terminate()
			return
		case "textDocument/didOpen", "textDocument/didChange", "textDocument/didClose":
			f.mu.Lock()
			params, _ := m["params"].(map[string]any)
			doc, _ := params["textDocument"].(map[string]any)
			uri, _ := doc["uri"].(string)
			f.events = append(f.events, strings.TrimPrefix(method, "textDocument/")+" "+uri)
			f.mu.Unlock()
		}
	}
}

func (f *routeServer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *routeServer) push(uri string, version any, diagnostics []map[string]any) {
	params := map[string]any{"uri": uri, "diagnostics": diagnostics}
	if version != nil {
		params["version"] = version
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params})
	f.send(string(body))
}

// routeBackend adds only the verified remote identity the production Engine
// withholds until the transport supplies one (see initializeDiagnostics).
type routeBackend struct {
	*engine.Engine
	host string
}

func (b routeBackend) RemoteCodeDiagnosticsHostID(context.Context) (string, error) {
	return b.host, nil
}

type routeFixture struct {
	t        *testing.T
	root     string
	engine   *engine.Engine
	server   *Server
	client   *Client
	tick     chan time.Time
	launches atomic.Int32
	servers  chan *routeServer
	all      []*routeServer
	mu       sync.Mutex
	events   []cd.Event
	attach   engine.CodeDiagnosticsAttachment
	ctx      context.Context
}

type routeOptions struct {
	noLaunch bool // ready=false: discovery without a launcher (old capability)
}

func newRouteFixture(t *testing.T, o routeOptions) *routeFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gopls-bin", "ts-bin", "a.go", "b.ts", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "route"}, CWD: dir, RepoRoot: dir}})
	x := &routeFixture{t: t, root: dir, tick: make(chan time.Time), servers: make(chan *routeServer, 8)}
	host, _ := (cd.HostIdentity{Kind: "ssh", Authority: "verified synthetic key/account", Endpoint: "verified synthetic endpoint/install"}).ID()
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		reg := func(id, bin string, owns func(string) (string, bool)) cd.Registration {
			state := cd.ProviderState{ProviderID: id, Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: cd.Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}}
			cfg := cd.ServerConfig{ProviderID: id, Executable: filepath.Join(root.Project().Root, bin), AdapterPolicy: "fake-only"}
			return cd.Registration{ProviderID: id, Availability: "available", Live: &cd.LiveRegistration{State: state, Server: cfg, Resolve: owns}}
		}
		return []cd.Registration{
			reg("gopls", "gopls-bin", func(p string) (string, bool) { return "go", strings.HasSuffix(p, ".go") }),
			reg("typescript", "ts-bin", cd.TSLanguageID),
		}, nil
	}
	session := cd.SessionOptions{After: func(time.Duration) <-chan time.Time { return make(chan time.Time) }}
	if !o.noLaunch {
		session.Launch = func(in cd.Invocation) (cd.SessionProcess, cd.SessionStreams, error) {
			x.launches.Add(1)
			inR, inW := io.Pipe()
			outR, outW := io.Pipe()
			errR, errW := io.Pipe()
			f := &routeServer{t: t, in: bufio.NewReader(inR), out: outW, errW: errW, exited: make(chan struct{}), texts: map[string]string{}}
			x.mu.Lock()
			x.all = append(x.all, f)
			x.mu.Unlock()
			x.servers <- f
			go f.serve()
			return f, cd.SessionStreams{Stdin: inW, Stdout: outR, Stderr: errR}, nil
		}
	}
	emit := engine.EmitterFunc(func(name string, payload any) {
		if x.server != nil {
			_ = x.server.Notify(name, payload)
		}
	})
	x.engine = engine.NewEngine(engine.WithService(engine.NewService(catalog, nil)), engine.WithCacheEnabled(false), engine.WithRoots(paths.Roots{Config: t.TempDir()}), engine.WithEmitter(emit),
		engine.WithInjectedLiveCodeDiagnostics(engine.LiveCodeDiagnosticsInjection{Host: host, Discover: discover, Session: session, Tick: x.tick}))
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	x.server, x.client = NewServer(routeBackend{x.engine, host}, inR, outW), NewClient(outR, inW)
	x.client.SetEmitter(engine.EmitterFunc(func(name string, payload any) {
		if ev, ok := payload.(cd.Event); ok && name == engine.CodeDiagnosticsUpdatedEvent {
			x.mu.Lock()
			x.events = append(x.events, ev)
			x.mu.Unlock()
		}
	}))
	done := make(chan struct{})
	go func() { defer close(done); _ = x.server.Serve(context.Background()) }()
	var cancel context.CancelFunc
	x.ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(func() {
		cancel()
		_ = x.client.Close()
		_ = x.server.Close()
		<-done
		_ = x.engine.Close()
	})
	return x
}

func (x *routeFixture) initialize(live bool) {
	x.t.Helper()
	init, err := x.client.Initialize(x.ctx, InitializeRequest{})
	if err != nil || !init.Capabilities.CodeDiagnostics || init.Capabilities.LiveCodeDiagnostics != live {
		x.t.Fatalf("capabilities %+v %v", init, err)
	}
}

func (x *routeFixture) attachAndApprove(approve bool) {
	x.t.Helper()
	a, err := x.client.AttachCodeDiagnostics(x.ctx, x.root, 1)
	if err != nil {
		x.t.Fatal(err)
	}
	x.attach = a
	var s cd.Snapshot
	eventually(x.t, "discovery proposal", func() bool {
		s, err = x.client.GetCodeDiagnosticsSnapshot(x.ctx, a.Context)
		return err == nil && s.Proposal != nil
	})
	if approve {
		if _, err = x.client.ApproveCodeDiagnostics(x.ctx, a.Context, "session", s.Proposal.Fingerprint); err != nil {
			x.t.Fatal(err)
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func (x *routeFixture) next() *routeServer {
	x.t.Helper()
	select {
	case f := <-x.servers:
		return f
	case <-time.After(15 * time.Second):
		x.t.Fatal("no server launched")
		return nil
	}
}

func (x *routeFixture) snapshot() cd.Snapshot {
	x.t.Helper()
	s, err := x.client.GetCodeDiagnosticsSnapshot(x.ctx, x.attach.Context)
	if err != nil {
		x.t.Fatal(err)
	}
	return s
}

func (x *routeFixture) uri(rel string) string {
	return fileURI(filepath.Join(x.root, rel))
}

// fileURI is the LSP form: a Windows drive path gets a leading slash
// (file:///C:/...), as the session sends it.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func (x *routeFixture) set(s cd.Snapshot, provider, path string) *cd.ResultSet {
	for i := range s.ResultSets {
		r := &s.ResultSets[i]
		if r.ProviderID == provider && r.Scope.Path == path {
			return r
		}
	}
	return nil
}

func (x *routeFixture) provider(s cd.Snapshot, id string) cd.ProviderState {
	for _, p := range s.Providers {
		if p.ProviderID == id {
			return p
		}
	}
	x.t.Fatalf("provider %s missing", id)
	return cd.ProviderState{}
}

// pump runs two pump iterations; the unbuffered channel makes the second send
// return only after the first iteration's emissions have completed.
func (x *routeFixture) pump() {
	x.t.Helper()
	for i := 0; i < 2; i++ {
		select {
		case x.tick <- time.Time{}:
		case <-time.After(10 * time.Second):
			x.t.Fatal("event pump not running")
		}
	}
	// Notifications cross a real pipe asynchronously.
	time.Sleep(20 * time.Millisecond)
}

func (x *routeFixture) lastEvent() (cd.Event, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.events) == 0 {
		return cd.Event{}, false
	}
	return x.events[len(x.events)-1], true
}

func lspDiagnostic(message string, line int) map[string]any {
	return map[string]any{"message": message, "severity": 1, "source": "fake", "range": cd.Range{Start: cd.Position{Line: line, Character: 1}, End: cd.Position{Line: line, Character: 3}}}
}

func TestRouteOldCapabilityOmitsLiveAndNeverLaunches(t *testing.T) {
	x := newRouteFixture(t, routeOptions{noLaunch: true})
	x.initialize(false)
	a, err := x.client.AttachCodeDiagnostics(x.ctx, x.root, 1)
	if err != nil {
		t.Fatal(err)
	}
	x.attach = a
	_, err = x.client.OpenCodeDiagnosticDocument(x.ctx, x.attach.Context, "a.go", "d1", 1, "package a", "r")
	if rpcCode(err) != "unsupported" || x.launches.Load() != 0 {
		t.Fatal(err, x.launches.Load())
	}
}

func TestRouteTrustDenialAndRevoke(t *testing.T) {
	x := newRouteFixture(t, routeOptions{})
	x.initialize(true)
	x.attachAndApprove(false)
	c := x.attach.Context
	if _, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, "a.go", "d1", 1, "package a", "r"); rpcCode(err) != "approvalRequired" || x.launches.Load() != 0 {
		t.Fatal("launched without approval", err)
	}
	s := x.snapshot()
	if _, err := x.client.ApproveCodeDiagnostics(x.ctx, c, "session", s.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, "a.go", "d1", 1, "package a", "r"); err != nil {
		t.Fatal(err)
	}
	x.next()
	revoked, err := x.client.RevokeCodeDiagnostics(x.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.client.OpenCodeDiagnosticDocument(x.ctx, revoked.Context, "a.go", "d2", 1, "package a", "r"); rpcCode(err) != "approvalRequired" {
		t.Fatal(err)
	}
	if x.launches.Load() != 1 {
		t.Fatal("revoked attachment launched")
	}
}

func TestRouteTwoProvidersPushesClearsReconcileAndBounds(t *testing.T) {
	x := newRouteFixture(t, routeOptions{})
	x.initialize(true)
	x.attachAndApprove(true)
	c := x.attach.Context
	open := func(path, id string) *routeServer {
		if _, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, path, id, 1, "text "+path, "r1"); err != nil {
			t.Fatal(err)
		}
		return x.next()
	}
	gopls := open("a.go", "da")
	ts := open("b.ts", "db")
	if x.launches.Load() != 2 {
		t.Fatal("two providers must launch two servers", x.launches.Load())
	}
	// Barrier: all pushes go through one serve goroutine per server, so a sentinel
	// push on the same server completes after the earlier ones.
	sentinel := func(srv *routeServer, rel string) {
		x.t.Helper()
		before := x.snapshot().Sequence
		srv.push(x.uri(rel), 1, []map[string]any{lspDiagnostic("sentinel "+strconv.Itoa(int(before)), 9)})
		eventually(x.t, "sentinel", func() bool { return x.snapshot().Sequence > before })
	}

	// Versioned push is current and attributed to its provider with a range.
	gopls.push(x.uri("a.go"), 1, []map[string]any{lspDiagnostic("<script>&😀", 0)})
	ts.push(x.uri("b.ts"), nil, []map[string]any{lspDiagnostic("unversioned", 0)})
	var s cd.Snapshot
	eventually(t, "both providers", func() bool {
		s = x.snapshot()
		return x.set(s, "gopls", "a.go") != nil && x.set(s, "typescript", "b.ts") != nil
	})
	g := x.set(s, "gopls", "a.go")
	if len(g.Diagnostics) != 1 || g.Diagnostics[0].Freshness != "current" || g.Diagnostics[0].Range == nil || g.Diagnostics[0].Basis.DocumentID != "da" || g.Diagnostics[0].Message != "<script>&😀" {
		t.Fatalf("versioned push: %+v", g)
	}
	u := x.set(s, "typescript", "b.ts")
	if len(u.Diagnostics) != 1 || u.Diagnostics[0].Freshness != "uncertain" || u.Diagnostics[0].Range == nil || u.Diagnostics[0].Basis.Version != nil || u.Diagnostics[0].Basis.ObservedVersion == nil || *u.Diagnostics[0].Basis.ObservedVersion != 1 {
		t.Fatalf("unversioned push became current: %+v", u)
	}
	// Events traverse pump -> Server.Notify -> Client emitter and match the snapshot.
	x.pump()
	if ev, ok := x.lastEvent(); !ok || ev.Context != c || x.set(ev.Snapshot, "gopls", "a.go") == nil {
		t.Fatal("event route delivered nothing usable", ev)
	}

	// Change to v2, then late/old/foreign pushes cannot replace current findings.
	if _, err := x.client.ChangeCodeDiagnosticDocument(x.ctx, c, "da", 2, "text a.go changed"); err != nil {
		t.Fatal(err)
	}
	before := x.snapshot().Sequence
	for _, v := range []any{1, 7, "bad"} {
		gopls.push(x.uri("a.go"), v, []map[string]any{})
	}
	gopls.push(x.uri("missing.go"), 2, []map[string]any{})
	sentinelDoc := "c.go"
	if _, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, sentinelDoc, "dc", 1, "text", "r1"); err != nil {
		t.Fatal(err)
	}
	gopls.push(x.uri(sentinelDoc), 1, []map[string]any{lspDiagnostic("barrier", 1)})
	eventually(t, "barrier", func() bool { return x.set(x.snapshot(), "gopls", sentinelDoc) != nil })
	s = x.snapshot()
	g = x.set(s, "gopls", "a.go")
	// The retained v1 finding keeps its own basis version (1) so consumers can
	// refuse to project it onto the v2 draft; none of the pushes replaced it.
	if g == nil || len(g.Diagnostics) != 1 || g.Diagnostics[0].Message != "<script>&😀" || g.Diagnostics[0].Basis.Version == nil || *g.Diagnostics[0].Basis.Version != 1 {
		t.Fatalf("late/old/foreign push altered findings: %+v", g)
	}
	if before == 0 {
		t.Fatal("no sequence progress")
	}
	// Empty current push clears only that document's set.
	gopls.push(x.uri("a.go"), 2, []map[string]any{})
	eventually(t, "empty clear", func() bool {
		s = x.snapshot()
		a := x.set(s, "gopls", "a.go")
		return a == nil || len(a.Diagnostics) == 0
	})
	if x.set(s, "typescript", "b.ts") == nil || x.set(s, "gopls", sentinelDoc) == nil {
		t.Fatal("empty clear crossed ownership")
	}

	// R06 bounds: oversize message is truncated on the wire with a notice.
	ts.push(x.uri("b.ts"), 1, []map[string]any{lspDiagnostic(strings.Repeat("m", 20000), 2)})
	eventually(t, "truncated message", func() bool {
		b := x.set(x.snapshot(), "typescript", "b.ts")
		return b != nil && len(b.Diagnostics) == 1 && len(b.Diagnostics[0].Message) <= 8192
	})
	if p := x.provider(x.snapshot(), "typescript"); p.Notice == "" || !p.Coverage.Partial {
		t.Fatal("truncation not surfaced", p)
	}

	// R10 reconciliation: invalidating a path drops findings and closes the document.
	inv, err := x.client.InvalidateCodeDiagnosticsPath(x.ctx, c, "b.ts")
	if err != nil {
		t.Fatal(err)
	}
	if x.set(inv, "typescript", "b.ts") != nil {
		t.Fatal("invalidated findings retained")
	}
	eventually(t, "didClose", func() bool {
		for _, e := range ts.seen() {
			if e == "didClose "+x.uri("b.ts") {
				return true
			}
		}
		return false
	})
	ts.push(x.uri("b.ts"), 1, []map[string]any{lspDiagnostic("zombie", 0)})
	sentinel(gopls, "c.go")
	time.Sleep(20 * time.Millisecond)
	if x.set(x.snapshot(), "typescript", "b.ts") != nil {
		t.Fatal("closed document resurrected")
	}

	// Failure: crashed gopls session surfaces failed + stale findings, other provider unaffected.
	_ = gopls.Terminate()
	eventually(t, "failure", func() bool {
		x.pump()
		return x.provider(x.snapshot(), "gopls").Phase == "failed"
	})
	s = x.snapshot()
	if p := x.provider(s, "gopls"); p.Failure == nil || p.Freshness != "stale" {
		t.Fatal("failure not surfaced", p)
	}
	if cdoc := x.set(s, "gopls", sentinelDoc); cdoc == nil || cdoc.Diagnostics[0].Freshness != "stale" {
		t.Fatal("findings not stale after failure", cdoc)
	}
	if x.provider(s, "typescript").Phase == "failed" {
		t.Fatal("failure crossed providers")
	}
}

func TestRouteSequenceGapResnapshotsOverRPC(t *testing.T) {
	x := newRouteFixture(t, routeOptions{})
	x.initialize(true)
	x.attachAndApprove(true)
	c := x.attach.Context
	if _, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, "a.go", "da", 1, "text", "r1"); err != nil {
		t.Fatal(err)
	}
	srv := x.next()
	start := x.snapshot().Sequence
	// 40 commits without any pump tick overflow the bounded pending queue.
	for i := 0; i < 40; i++ {
		srv.push(x.uri("a.go"), 1, []map[string]any{lspDiagnostic("m"+strconv.Itoa(i), i)})
	}
	eventually(t, "all pushes", func() bool { return x.snapshot().Sequence >= start+40 })
	x.pump()
	ev, ok := x.lastEvent()
	if !ok {
		t.Fatal("no event after gap")
	}
	current := x.snapshot()
	if ev.Snapshot.Sequence != current.Sequence || x.set(ev.Snapshot, "gopls", "a.go") == nil || ev.Snapshot.Sequence < start+40 {
		t.Fatalf("gap not resnapshotted: event %d current %d", ev.Snapshot.Sequence, current.Sequence)
	}
	if got := x.set(ev.Snapshot, "gopls", "a.go").Diagnostics; len(got) != 1 || got[0].Message != "m39" {
		t.Fatal("gap resnapshot is not authoritative", got)
	}
}

// recordedStep is one frontend-visible call result plus the events the real Go
// route delivered to the rpc.Client emitter after it.
type recordedStep struct {
	Name     string     `json:"name"`
	Response any        `json:"response,omitempty"`
	Error    string     `json:"error,omitempty"`
	Events   []cd.Event `json:"events"`
}

// TestRouteRecordFrontendFixture drives the full Go route once and records the
// exact calls/events for frontend/test/live-route-integration.test.mjs. It only
// writes when R14_RECORD is set (go test is otherwise free of file writes); the
// committed fixture is a recording, not a live cross-language pipe.
func TestRouteRecordFrontendFixture(t *testing.T) {
	target := os.Getenv("R14_RECORD")
	if target == "" {
		t.Skip("set R14_RECORD=<path> to regenerate the frontend fixture")
	}
	x := newRouteFixture(t, routeOptions{})
	x.initialize(true)
	var steps []recordedStep
	taken := 0
	record := func(name string, response any, err error) {
		x.t.Helper()
		x.pump()
		want := x.snapshot().Sequence
		eventually(x.t, fmt.Sprintf("events caught up %s want %d", name, want), func() bool { ev, ok := x.lastEvent(); return ok && ev.Sequence >= want })
		x.mu.Lock()
		events := append([]cd.Event{}, x.events[taken:]...)
		taken = len(x.events)
		x.mu.Unlock()
		s := recordedStep{Name: name, Response: response, Events: events}
		if err != nil {
			s.Error = rpcCode(err)
		}
		steps = append(steps, s)
	}
	a, err := x.client.AttachCodeDiagnostics(x.ctx, x.root, 1)
	if err != nil {
		t.Fatal(err)
	}
	x.attach = a
	var s cd.Snapshot
	eventually(t, "proposal", func() bool {
		s, err = x.client.GetCodeDiagnosticsSnapshot(x.ctx, a.Context)
		return err == nil && s.Proposal != nil
	})
	a.Snapshot = s
	record("attach", a, nil)
	c := a.Context
	_, err = x.client.OpenCodeDiagnosticDocument(x.ctx, c, "a.go", "da", 1, "denied", "r1")
	record("openDenied", nil, err)
	approved, err := x.client.ApproveCodeDiagnostics(x.ctx, c, "session", s.Proposal.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	record("approve", approved, nil)
	text := "a😀\r\nb"
	r, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, "a.go", "da", 1, text, "r1")
	if err != nil {
		t.Fatal(err)
	}
	record("open", r, nil)
	gopls := x.next()
	// A second provider's document so attribution across providers is recorded.
	if _, err = x.client.OpenCodeDiagnosticDocument(x.ctx, c, "b.ts", "db", 1, "b", "r1"); err != nil {
		t.Fatal(err)
	}
	ts := x.next()
	record("openSecond", nil, nil)
	wait := func(cond func(cd.Snapshot) bool) {
		t.Helper()
		eventually(t, "state", func() bool { return cond(x.snapshot()) })
	}
	diag := func(msg string) []map[string]any { return []map[string]any{lspDiagnostic(msg, 0)} }
	gopls.push(x.uri("a.go"), 1, diag("<script>&😀"))
	ts.push(x.uri("b.ts"), 1, diag("ts finding"))
	wait(func(s cd.Snapshot) bool {
		return x.set(s, "gopls", "a.go") != nil && x.set(s, "typescript", "b.ts") != nil
	})
	record("pushV1", nil, nil)
	ch, err := x.client.ChangeCodeDiagnosticDocument(x.ctx, c, "da", 3, text+"!")
	if err != nil {
		t.Fatal(err)
	}
	record("change", ch, nil)
	// Late (old version) and wrong-document publications: no committed change.
	gopls.push(x.uri("a.go"), 1, diag("late"))
	gopls.push(x.uri("missing.go"), 3, diag("foreign"))
	gopls.push(x.uri("a.go"), 3, diag("<script>&😀 v2"))
	wait(func(s cd.Snapshot) bool {
		g := x.set(s, "gopls", "a.go")
		return g != nil && len(g.Diagnostics) == 1 && g.Diagnostics[0].Message == "<script>&😀 v2"
	})
	record("pushLateThenV2", nil, nil)
	gopls.push(x.uri("a.go"), nil, diag("unversioned"))
	wait(func(s cd.Snapshot) bool {
		g := x.set(s, "gopls", "a.go")
		return g != nil && len(g.Diagnostics) == 1 && g.Diagnostics[0].Message == "unversioned"
	})
	record("pushUnversioned", nil, nil)
	gopls.push(x.uri("a.go"), 3, []map[string]any{})
	wait(func(s cd.Snapshot) bool { g := x.set(s, "gopls", "a.go"); return g == nil || len(g.Diagnostics) == 0 })
	record("emptyClear", nil, nil)
	ts.push(x.uri("b.ts"), 1, []map[string]any{lspDiagnostic(strings.Repeat("m", 20000), 2)})
	wait(func(s cd.Snapshot) bool {
		b := x.set(s, "typescript", "b.ts")
		return x.provider(s, "typescript").Notice != "" && b != nil && len(b.Diagnostics) == 1 && len(b.Diagnostics[0].Message) > 1000
	})
	record("pushTruncated", nil, nil)
	inv, err := x.client.InvalidateCodeDiagnosticsPath(x.ctx, c, "b.ts")
	if err != nil {
		t.Fatal(err)
	}
	record("invalidate", inv, nil)
	gopls.push(x.uri("a.go"), 3, diag("before crash"))
	wait(func(s cd.Snapshot) bool { g := x.set(s, "gopls", "a.go"); return g != nil && len(g.Diagnostics) == 1 })
	record("pushBeforeCrash", nil, nil)
	_ = gopls.Terminate()
	eventually(t, "failure", func() bool {
		x.pump()
		return x.provider(x.snapshot(), "gopls").Phase == "failed"
	})
	record("crash", nil, nil)
	data, err := json.MarshalIndent(map[string]any{"host": a.Project.HostID, "root": a.Project.Root, "steps": steps}, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(target, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
