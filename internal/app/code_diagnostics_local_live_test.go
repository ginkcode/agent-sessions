package app

import (
	"bufio"
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

// Local live route (CD-22): App.ConnectionState local capabilities -> attach ->
// proposal -> approve -> open draft -> real SessionManager/LSP transport/gopls
// bridge -> framed server push -> codeDiagnostics:updated event. Only the
// language-server process is fake; the Engine derives its own local host
// identity from a fixed fixture machine ID so the route runs on every CI OS.
type localLSP struct {
	in     *bufio.Reader
	out    *io.PipeWriter
	errW   *io.PipeWriter
	exited chan struct{}
	once   sync.Once
	opened chan string
}

func (f *localLSP) Wait() (int, error) { <-f.exited; return 0, nil }
func (f *localLSP) Terminate() error {
	f.once.Do(func() { close(f.exited) })
	_ = f.out.Close()
	_ = f.errW.Close()
	return nil
}
func (f *localLSP) Close() error { return nil }

func (f *localLSP) send(body string) {
	_, _ = io.WriteString(f.out, fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body))
}

func (f *localLSP) serve() {
	for {
		length := -1
		for {
			line, err := f.in.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
				length, _ = strconv.Atoi(v)
			}
		}
		body := make([]byte, max(length, 0))
		if _, err := io.ReadFull(f.in, body); err != nil {
			return
		}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		id, _ := json.Marshal(m["id"])
		switch m["method"] {
		case "initialize":
			f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"capabilities":{"textDocumentSync":{"openClose":true,"change":2,"save":true}}}}`)
		case "shutdown":
			f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":null}`)
		case "exit":
			_ = f.Terminate()
			return
		case "textDocument/didOpen":
			params, _ := m["params"].(map[string]any)
			doc, _ := params["textDocument"].(map[string]any)
			uri, _ := doc["uri"].(string)
			f.opened <- uri
		}
	}
}

func (f *localLSP) push(uri string, version int, messages ...string) {
	diags := []map[string]any{}
	for _, msg := range messages {
		diags = append(diags, map[string]any{"message": msg, "severity": 1, "source": "compiler", "range": cd.Range{Start: cd.Position{Line: 2, Character: 0}, End: cd.Position{Line: 2, Character: 1}}})
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": uri, "version": version, "diagnostics": diags}})
	f.send(string(body))
}

func TestLocalLiveRouteCapabilitiesApproveOpenAndEvent(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gopls-bin", "a.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "local-live"}, CWD: dir, RepoRoot: dir}})
	var launches atomic.Int32
	servers := make(chan *localLSP, 2)
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		state := cd.ProviderState{ProviderID: "gopls", Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: cd.Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}}
		cfg := cd.ServerConfig{ProviderID: "gopls", Executable: filepath.Join(root.Project().Root, "gopls-bin"), AdapterPolicy: "fake-only"}
		return []cd.Registration{{ProviderID: "gopls", Availability: "available", Live: &cd.LiveRegistration{State: state, Server: cfg, Resolve: func(p string) (string, bool) { return "go", strings.HasSuffix(p, ".go") }}}}, nil
	}
	launch := func(cd.Invocation) (cd.SessionProcess, cd.SessionStreams, error) {
		launches.Add(1)
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		errR, errW := io.Pipe()
		f := &localLSP{in: bufio.NewReader(inR), out: outW, errW: errW, exited: make(chan struct{}), opened: make(chan string, 4)}
		go f.serve()
		servers <- f
		return f, cd.SessionStreams{Stdin: inW, Stdout: outR, Stderr: errR}, nil
	}
	var mu sync.Mutex
	var events []cd.Event
	emit := engine.EmitterFunc(func(name string, payload any) {
		if ev, ok := payload.(cd.Event); ok && name == engine.CodeDiagnosticsUpdatedEvent {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		}
	})
	config := t.TempDir()
	// Empty Host: the injection uses the Engine's own local identity, exactly
	// like the production local route; discovery, launch and the platform
	// machine-ID source are fakes.
	eng := engine.NewEngine(engine.WithService(engine.NewService(catalog, nil)), engine.WithCacheEnabled(false), engine.WithRoots(paths.Roots{Config: config}), engine.WithEmitter(emit), engine.WithFixtureMachineIdentity(testMachineID),
		engine.WithInjectedLiveCodeDiagnostics(engine.LiveCodeDiagnosticsInjection{Discover: discover, Session: cd.SessionOptions{Launch: launch}}))
	t.Cleanup(func() { _ = eng.Close() })
	a := &App{localEngine: eng, backend: eng, roots: paths.Roots{Config: config}}

	state := a.ConnectionState()
	if state.Phase != ConnLocal || state.HostID == "" || !state.Capabilities.CodeDiagnostics || !state.Capabilities.LiveCodeDiagnostics {
		t.Fatalf("local capabilities not advertised: %+v", state)
	}
	host := state.HostID
	att, err := a.AttachCodeDiagnostics(dir, 1, host)
	if err != nil {
		t.Fatal(err)
	}
	var snap cd.Snapshot
	waitFor(t, "proposal", func() bool {
		snap, err = a.GetCodeDiagnosticsSnapshot(att.Context, host)
		return err == nil && snap.Proposal != nil
	})
	_, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d1", 1, "package a\n\nfunc {", "r1", host)
	checkDiagnosticsError(t, err, "approvalRequired")
	if launches.Load() != 0 {
		t.Fatal("launched before approval")
	}
	if _, err = a.ApproveCodeDiagnostics(att.Context, "session", snap.Proposal.Fingerprint, host); err != nil {
		t.Fatal(err)
	}
	if _, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d1", 1, "package a\n\nfunc {", "r1", host); err != nil {
		t.Fatal(err)
	}
	var srv *localLSP
	select {
	case srv = <-servers:
	case <-time.After(15 * time.Second):
		t.Fatal("no server launched")
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(dir, "a.go"))}).String()
	select {
	case got := <-srv.opened:
		if got != uri {
			t.Fatal("didOpen uri", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no didOpen")
	}
	srv.push(uri, 1, "expected 'IDENT', found '{'")
	waitFor(t, "diagnostic event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range events {
			if ev.Context != att.Context {
				continue
			}
			for _, rs := range ev.Snapshot.ResultSets {
				if rs.ProviderID == "gopls" && rs.Scope.Path == "a.go" && len(rs.Diagnostics) == 1 && rs.Diagnostics[0].Freshness == "current" && rs.Diagnostics[0].Basis.DocumentID == "d1" {
					return true
				}
			}
		}
		return false
	})
	if _, err = a.ChangeCodeDiagnosticDocument(att.Context, "d1", 2, "package a\n", host); err != nil {
		t.Fatal(err)
	}
	srv.push(uri, 2)
	waitFor(t, "empty clear", func() bool {
		s, err := a.GetCodeDiagnosticsSnapshot(att.Context, host)
		if err != nil {
			return false
		}
		for _, rs := range s.ResultSets {
			if rs.ProviderID == "gopls" && rs.Scope.Path == "a.go" && len(rs.Diagnostics) > 0 {
				return false
			}
		}
		return true
	})
	if launches.Load() != 1 {
		t.Fatal("unexpected launches", launches.Load())
	}
}

// The explicit-service App (tests, embedders) never enables the local live
// route: Stage 1 remains advertised, live does not.
func TestLocalCapabilitiesWithoutLiveRoute(t *testing.T) {
	config := t.TempDir()
	eng := engine.NewEngine(engine.WithService(engine.NewService(scan.NewCatalog(), nil)), engine.WithCacheEnabled(false), engine.WithRoots(paths.Roots{Config: config}), engine.WithFixtureMachineIdentity(testMachineID))
	t.Cleanup(func() { _ = eng.Close() })
	a := &App{localEngine: eng, backend: eng, roots: paths.Roots{Config: config}}
	state := a.ConnectionState()
	if state.HostID == "" || !state.Capabilities.CodeDiagnostics || state.Capabilities.LiveCodeDiagnostics {
		t.Fatalf("live advertised without a route: %+v", state)
	}
	if NewAppWithService(nil).localLive {
		t.Fatal("explicit-service App enabled the local live route")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}
