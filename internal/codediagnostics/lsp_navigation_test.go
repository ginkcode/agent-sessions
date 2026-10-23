package codediagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const navigationInit = `{"capabilities":{"textDocumentSync":{"openClose":true,"change":2,"save":true},"definitionProvider":true,"referencesProvider":{"workDoneProgress":false},"hoverProvider":true}}`

// navServer answers navigation requests from a table the test changes;
// a method without an answer is never answered.
type navServer struct {
	mu       sync.Mutex
	answers  map[string]string // method -> JSON-RPC result or error member
	requests []map[string]any
	cancels  int
}

func (n *navServer) set(method, answer string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.answers[method] = answer
}

func (n *navServer) last() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.requests[len(n.requests)-1]["params"].(map[string]any)
}

func (n *navServer) script(f *fakeSrv) {
	for {
		m, ok := f.read()
		if !ok {
			return
		}
		id, _ := json.Marshal(m["id"])
		method, _ := m["method"].(string)
		reply := func(member string) { f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,` + member + `}`) }
		switch {
		case method == "initialize":
			reply(`"result":` + navigationInit)
		case method == "shutdown":
			reply(`"result":null`)
		case method == "exit":
			_ = f.Terminate()
			return
		case method == "$/cancelRequest":
			n.mu.Lock()
			n.cancels++
			n.mu.Unlock()
		case strings.HasPrefix(method, "textDocument/") && m["id"] != nil:
			n.mu.Lock()
			n.requests = append(n.requests, m)
			answer, ok := n.answers[method]
			n.mu.Unlock()
			if ok {
				reply(answer)
			}
		}
	}
}

func navRange(sl, sc, el, ec int) string {
	return fmt.Sprintf(`{"start":{"line":%d,"character":%d},"end":{"line":%d,"character":%d}}`, sl, sc, el, ec)
}

func TestNavigationAgainstFakeServer(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	cfg.ProviderID = goplsProviderID
	e.approve(root, cfg)
	srv := &navServer{answers: map[string]string{}}
	e.script = srv.script
	dir := root.Project().Root
	for _, name := range []string{"source.go", "other.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "fmt", "print.go")
	docs := SessionDocuments{Sessions: e.mgr, Resolve: func(AuthorizedRoot, string) (DocumentServer, bool) {
		return DocumentServer{Config: cfg, LanguageID: "go"}, true
	}}
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	e.next()
	m := NewManagerForTest(t, root)
	m.SetDocumentProvider(docs)
	ctx, c := context.Background(), m.snap.Context
	text := "package a\n\nfunc main() { helper() }\n"
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, text, ""); err != nil {
		t.Fatal(err)
	}
	at := Position{Line: 2, Character: 15}

	// Links: the name's range inside the root, an absolute path outside it;
	// other schemes and missing files under the root are left out.
	srv.set("textDocument/definition", `"result":[`+
		`{"targetUri":"`+uriOf(root, "other.go")+`","targetRange":`+navRange(0, 0, 3, 0)+`,"targetSelectionRange":`+navRange(1, 5, 1, 11)+`},`+
		`{"targetUri":"`+fileURI(outside)+`","targetRange":`+navRange(10, 0, 12, 1)+`},`+
		`{"targetUri":"untitled:Untitled-1","targetSelectionRange":`+navRange(0, 0, 0, 1)+`},`+
		`{"targetUri":"`+uriOf(root, "missing.go")+`","targetSelectionRange":`+navRange(0, 0, 0, 1)+`},`+
		`{"targetUri":"`+uriOf(root, "other.go")+`","targetSelectionRange":`+navRange(2, 1, 1, 0)+`}]`)
	got, err := m.Navigate(ctx, c, "d1", 1, NavigateDefinition, at)
	if err != nil {
		t.Fatal(err)
	}
	want := []NavigationLocation{
		{Path: "other.go", Range: Range{Position{1, 5}, Position{1, 11}}},
		{Path: outside, Range: Range{Position{10, 0}, Position{12, 1}}, External: true},
	}
	if got.Op != NavigateDefinition || got.DocumentID != "d1" || got.Version != 1 || got.Truncated || fmt.Sprint(got.Locations) != fmt.Sprint(want) {
		t.Fatalf("%+v", got)
	}
	params := srv.last()
	if params["textDocument"].(map[string]any)["uri"] != uriOf(root, "source.go") || params["position"].(map[string]any)["character"].(float64) != 15 {
		t.Fatalf("%v", params)
	}

	// A single Location, and no answer at all.
	srv.set("textDocument/definition", `"result":{"uri":"`+uriOf(root, "source.go")+`","range":`+navRange(2, 5, 2, 9)+`}`)
	if got, err = m.Navigate(ctx, c, "d1", 1, NavigateDefinition, at); err != nil || len(got.Locations) != 1 || got.Locations[0].Path != "source.go" {
		t.Fatal(got, err)
	}
	srv.set("textDocument/definition", `"result":null`)
	if got, err = m.Navigate(ctx, c, "d1", 1, NavigateDefinition, at); err != nil || got.Locations == nil || len(got.Locations) != 0 {
		t.Fatal(got, err)
	}

	// References include the declaration and stop at the cap.
	var refs []string
	for i := 0; i <= maxNavigationLocations; i++ {
		refs = append(refs, `{"uri":"`+uriOf(root, "other.go")+`","range":`+navRange(i, 0, i, 4)+`}`)
	}
	srv.set("textDocument/references", `"result":[`+strings.Join(refs, ",")+`]`)
	if got, err = m.Navigate(ctx, c, "d1", 1, NavigateReferences, at); err != nil || len(got.Locations) != maxNavigationLocations || !got.Truncated {
		t.Fatal(len(got.Locations), got.Truncated, err)
	}
	if srv.last()["context"].(map[string]any)["includeDeclaration"] != true {
		t.Fatal("declaration not included")
	}

	// Hover: Markdown capped at 64 KB, marked strings fenced, plain text kept.
	long := strings.Repeat("é", maxHoverBytes)
	srv.set("textDocument/hover", `"result":{"contents":{"kind":"markdown","value":"`+long+`"},"range":`+navRange(2, 14, 2, 20)+`}`)
	if got, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, at); err != nil || got.Hover == nil || got.Hover.Kind != "markdown" || len(got.Hover.Value) != maxHoverBytes || !got.Truncated || got.Hover.Range == nil {
		t.Fatal(got.Hover != nil, got.Truncated, err)
	}
	srv.set("textDocument/hover", `"result":{"contents":[{"language":"go","value":"func helper()"},"Helper does it."]}`)
	if got, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, at); err != nil || got.Hover.Value != "```go\nfunc helper()\n```\n\nHelper does it." || got.Truncated {
		t.Fatal(got.Hover, err)
	}
	srv.set("textDocument/hover", `"result":{"contents":{"kind":"plaintext","value":"var x int"}}`)
	if got, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, at); err != nil || *got.Hover != (HoverText{Kind: "plaintext", Value: "var x int"}) {
		t.Fatal(got.Hover, err)
	}

	// Requests for another version, place or operation are refused before
	// reaching the server.
	before := len(srv.requests)
	if _, err = m.Navigate(ctx, c, "d1", 2, NavigateHover, at); errCode(err) != "staleVersion" {
		t.Fatal(err)
	}
	if _, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, Position{Line: 9}); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
	if _, err = m.Navigate(ctx, c, "d1", 1, "rename", at); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
	if _, err = m.Navigate(ctx, c, "nope", 1, NavigateHover, at); errCode(err) != "notFound" {
		t.Fatal(err)
	}
	if len(srv.requests) != before {
		t.Fatal("refused request reached the server")
	}

	// The server's own errors.
	srv.set("textDocument/hover", `"error":{"code":-32801,"message":"content modified"}`)
	if _, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, at); errCode(err) != "staleVersion" {
		t.Fatal(err)
	}
	srv.set("textDocument/hover", `"error":{"code":-32603,"message":"SECRET"}`)
	if _, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, at); errCode(err) != "toolFailed" || strings.Contains(err.Error(), "SECRET") {
		t.Fatal(err)
	}

	// No answer in ten seconds: timeout, and the server is told to cancel.
	srv.mu.Lock()
	delete(srv.answers, "textDocument/references")
	srv.mu.Unlock()
	timers := e.clock.count(navigationTimeout)
	done := make(chan error, 1)
	go func() { _, err := m.Navigate(ctx, c, "d1", 1, NavigateReferences, at); done <- err }()
	eventually(t, "navigation timer", func() bool { return e.clock.count(navigationTimeout) > timers })
	e.clock.Advance(navigationTimeout)
	if err = <-done; errCode(err) != "timeout" {
		t.Fatal(err)
	}
	eventually(t, "cancel sent", func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.cancels == 1 })

	// A stopped server is not running.
	e.mgr.Revoke(root)
	if _, err = m.Navigate(ctx, c, "d1", 1, NavigateHover, at); errCode(err) != "notRunning" {
		t.Fatal(err)
	}
}

// navDocs adds navigation to the fake document provider.
type navDocs struct {
	MultiOwnedDocumentProvider
	mu        sync.Mutex
	reqs      []NavigationRequest
	locations []NavigationLocation // the answer; source.go when nil
}

func (n *navDocs) Navigate(_ context.Context, req NavigationRequest) (NavigationResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reqs = append(n.reqs, req)
	if n.locations != nil {
		return NavigationResult{Locations: n.locations}, nil
	}
	return NavigationResult{Locations: []NavigationLocation{{Path: "source.go"}}}, nil
}

func TestNavigationChoosesTheLanguageServer(t *testing.T) {
	m, s, p := multiDocFixture(t)
	ruffCaps, pyrightCaps := incrementalCaps, incrementalCaps
	ruffCaps.Hover = true
	pyrightCaps.Definition, pyrightCaps.References, pyrightCaps.Hover = true, true, true
	p.entries[0].Capabilities, p.entries[1].Capabilities = ruffCaps, pyrightCaps
	ctx, c := context.Background(), s.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "x = 1\n", ""); err != nil {
		t.Fatal(err)
	}
	// Without a navigation provider there is nothing to ask.
	if _, err := m.Navigate(ctx, c, "d1", 1, NavigateHover, Position{}); errCode(err) != "unsupported" {
		t.Fatal(err)
	}
	nav := &navDocs{MultiOwnedDocumentProvider: p}
	m.SetDocumentProvider(nav)
	for _, op := range []string{NavigateHover, NavigateDefinition} {
		got, err := m.Navigate(ctx, c, "d1", 1, op, Position{Line: 0, Character: 4})
		if err != nil || got.Op != op || len(got.Locations) != 1 {
			t.Fatal(got, err)
		}
		if req := nav.reqs[len(nav.reqs)-1]; req.Owner.ProviderID != "pyright" || req.Generation != "g-pyright" || req.Path != "source.go" {
			t.Fatalf("%s went to %+v", op, req)
		}
	}
	// Only Ruff runs: hover still goes there, definitions are not offered.
	p.entries[1].Err = failure("staleContext", "Language session is not running")
	if _, err := m.Navigate(ctx, c, "d1", 1, NavigateHover, Position{}); err != nil || nav.reqs[len(nav.reqs)-1].Owner.ProviderID != "ruff" {
		t.Fatal(err)
	}
	if _, err := m.Navigate(ctx, c, "d1", 1, NavigateDefinition, Position{}); errCode(err) != "notRunning" {
		t.Fatal(err)
	}
	// Pyright restarted and has not seen the document yet.
	p.entries[1].Err, p.entries[1].Generation = nil, "g-pyright-2"
	if _, err := m.Navigate(ctx, c, "d1", 1, NavigateDefinition, Position{}); errCode(err) != "resyncRequired" {
		t.Fatal(err)
	}
}

func TestNavigationNeedsTheProviderCapability(t *testing.T) {
	m, s, f := docFixture(t, incrementalCaps)
	nav := &navDocs{MultiOwnedDocumentProvider: &multiDocs{fakeDocs: f, entries: []DocumentSession{{Owner: DocumentOwnerIdentity{goplsProviderID, "fake-policy"}, Generation: "g1", Capabilities: incrementalCaps}}}}
	m.SetDocumentProvider(nav)
	ctx, c := context.Background(), s.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "package a\n", ""); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{NavigateDefinition, NavigateReferences, NavigateHover} {
		if _, err := m.Navigate(ctx, c, "d1", 1, op, Position{}); errCode(err) != "unsupported" {
			t.Fatal(op, err)
		}
	}
	if len(nav.reqs) != 0 {
		t.Fatal("asked a server without the capability")
	}
}

func TestNegotiateNavigationCapabilities(t *testing.T) {
	for raw, want := range map[string][3]bool{
		`{}`: {},
		`{"definitionProvider":true,"referencesProvider":{},"hoverProvider":{"workDoneProgress":true}}`: {true, true, true},
		`{"definitionProvider":false,"referencesProvider":null,"hoverProvider":true}`:                   {false, false, true},
	} {
		var caps map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &caps)
		caps["textDocumentSync"] = json.RawMessage(`1`)
		body, _ := json.Marshal(map[string]any{"capabilities": caps})
		got, err := negotiateCapabilities(body)
		if err != nil || [3]bool{got.Definition, got.References, got.Hover} != want {
			t.Fatal(raw, got, err)
		}
	}
}

// The navigation capabilities describe what the client reads; approving the
// same server again is not needed.
func TestNavigationLeavesTheApprovalPolicyAlone(t *testing.T) {
	cfg := ServerConfig{ProviderID: goplsProviderID, Executable: "/x/gopls", Args: []string{"serve"}, AdapterPolicy: "lsp-v1", DeclareDiagnostics: true, Settings: []byte(`{"a":1}`)}
	const want = `{"Version":1,"Command":{"ProviderID":"gopls","Executable":"/x/gopls","Args":["serve"],"Env":null,"Directory":"","AdapterPolicy":"lsp-v1|client:publish-diagnostics|settings:{\"a\":1}","AcceptedExits":[0]},"Environment":["GOTOOLCHAIN=local","GOPROXY=off","GOSUMDB=off","npm_config_offline=true","npm_config_ignore_scripts=true","COREPACK_ENABLE_NETWORK=0"]}`
	if got := cfg.Policy(); got != want {
		t.Fatalf("policy changed:\n%s", got)
	}
}
