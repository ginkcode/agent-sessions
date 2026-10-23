package codediagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func goplsRoot(t *testing.T, files map[string]string) AuthorizedRoot {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := AuthorizeRoot("local:test", dir, func(s string) bool { return s == dir })
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func uriOf(root AuthorizedRoot, rel string) string {
	return fileURI(filepath.Join(root.Project().Root, filepath.FromSlash(rel)))
}

func TestGoplsEvidenceMatchesSupportedVersion(t *testing.T) {
	data, err := os.ReadFile("testdata/gopls/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Versions   map[string]string
		Initialize struct {
			Capabilities struct {
				TextDocumentSync map[string]any
				PositionEncoding string
			}
		}
		Scenarios []struct {
			ID           string
			Publications []struct {
				Params struct {
					Version     *int64
					Diagnostics []json.RawMessage
				}
			}
		}
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Versions["gopls"] == "" {
		t.Fatalf("evidence names no gopls version")
	}
	sync := ev.Initialize.Capabilities.TextDocumentSync
	if sync["openClose"] != true || sync["change"] != float64(2) {
		t.Fatalf("sync %v", sync)
	}
	versioned := map[string]bool{}
	for _, s := range ev.Scenarios {
		for _, p := range s.Publications {
			versioned[s.ID] = p.Params.Version != nil
		}
	}
	// Observed: pushes for open documents carry the version; didClose pushes do not.
	if !versioned["open-error"] || !versioned["change-fix"] || versioned["close"] {
		t.Fatalf("version observations changed: %v", versioned)
	}
	// The recorded fixture payloads must normalize with the production parser.
	root := goplsRoot(t, map[string]string{"main.go": "package main\n"})
	var raw struct {
		Scenarios []struct {
			Publications []struct {
				Params json.RawMessage
			}
		}
	}
	_ = json.Unmarshal(data, &raw)
	got := 0
	for _, s := range raw.Scenarios {
		for _, p := range s.Publications {
			if !strings.Contains(string(p.Params), `"uri"`) {
				continue
			}
			fixed := strings.ReplaceAll(string(p.Params), "file:///fixture/", uriOf(root, "")+"/")
			pub, err := normalizeGoplsPublication(root, json.RawMessage(fixed))
			if err != nil || pub.Path != "main.go" {
				t.Fatalf("%v %+v", err, pub)
			}
			for _, d := range pub.Diagnostics {
				if d.Severity != "error" || d.Source != "gopls/compiler" || d.Code != "IncompatibleAssign" || d.Range == nil || d.Range.End.Character != 18 {
					t.Fatalf("%+v", d)
				}
			}
			got++
		}
	}
	if got < 4 {
		t.Fatal("expected recorded publications", got)
	}
}

func TestNormalizeGoplsPublication(t *testing.T) {
	root := goplsRoot(t, map[string]string{"a.go": "x"})
	u := uriOf(root, "a.go")
	pub := func(body string) (goplsPublication, error) {
		return normalizeGoplsPublication(root, json.RawMessage(body))
	}
	good := `{"uri":"` + u + `","version":3,"diagnostics":[
	 {"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":4}},"severity":1,"code":"E1","source":"compiler","message":"bad 😀 value"},
	 {"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"severity":2,"code":7,"message":"n"},
	 {"severity":3,"message":"no range"},
	 {"range":{"start":{"line":5,"character":0},"end":{"line":4,"character":0}},"severity":4,"message":"reversed"},
	 {"range":{"start":{"line":-1,"character":0},"end":{"line":0,"character":0}},"message":"negative"},
	 {"range":{"start":{"line":0,"character":0}},"message":"incomplete range"},
	 {"severity":9,"message":"unknown severity"},
	 {"message":""},
	 {"message":"no severity defaults to warning"}
	]}`
	p, err := pub(good)
	if err != nil || p.Path != "a.go" || p.Version == nil || *p.Version != 3 {
		t.Fatalf("%v %+v", err, p)
	}
	if p.Dropped != 2 || len(p.Diagnostics) != 7 {
		t.Fatalf("dropped %d kept %d", p.Dropped, len(p.Diagnostics))
	}
	byMessage := map[string]Diagnostic{}
	for _, d := range p.Diagnostics {
		byMessage[d.Message] = d
	}
	d := []Diagnostic{byMessage["bad 😀 value"], byMessage["n"], byMessage["no range"], byMessage["reversed"], byMessage["negative"], byMessage["incomplete range"], byMessage["no severity defaults to warning"]}
	if d[0].Severity != "error" || d[0].Source != "gopls/compiler" || d[0].Code != "E1" || d[0].Range.End.Character != 4 || d[0].Basis.Kind != "document" {
		t.Fatalf("%+v", d[0])
	}
	if d[1].Code != "7" || d[1].Source != "gopls" || d[1].Severity != "warning" || d[2].Severity != "information" || d[2].Range != nil || d[2].LocationIssue != "" {
		t.Fatalf("%+v %+v", d[1], d[2])
	}
	for _, i := range []int{3, 4, 5} {
		if d[i].Range != nil || d[i].LocationIssue != "invalidRange" {
			t.Fatalf("%d %+v", i, d[i])
		}
	}
	if d[3].Severity != "hint" || d[6].Severity != "warning" {
		t.Fatalf("%+v %+v", d[3], d[6])
	}

	// Unversioned and empty publications are valid (empty clears).
	p, err = pub(`{"uri":"` + u + `","diagnostics":[]}`)
	if err != nil || p.Version != nil || p.Diagnostics == nil || len(p.Diagnostics) != 0 {
		t.Fatalf("%v %+v", err, p)
	}
	for name, body := range map[string]string{
		"missing diagnostics": `{"uri":"` + u + `"}`,
		"bad version":         `{"uri":"` + u + `","version":"x","diagnostics":[]}`,
		"zero version":        `{"uri":"` + u + `","version":0,"diagnostics":[]}`,
		"not json":            `[`,
		"http":                `{"uri":"http://example.test/a.go","diagnostics":[]}`,
		"host":                `{"uri":"file://host/tmp/a.go","diagnostics":[]}`,
		"query":               `{"uri":"` + u + `?x=1","diagnostics":[]}`,
		"relative":            `{"uri":"a.go","diagnostics":[]}`,
		"empty uri":           `{"uri":"","diagnostics":[]}`,
	} {
		if _, err := pub(body); !errors.Is(err, errGoplsInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, uri := range map[string]string{
		"outside":   "file:///etc/passwd",
		"parent":    fileURI(filepath.Dir(root.Project().Root) + "/other.go"),
		"traversal": strings.TrimSuffix(u, "a.go") + "../a.go",
	} {
		_, err := pub(`{"uri":"` + uri + `","diagnostics":[{"message":"secret"}]}`)
		if name == "traversal" {
			if err == nil {
				t.Errorf("traversal accepted")
			}
			continue
		}
		if !errors.Is(err, errGoplsOutside) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A contained but missing file is not linkable.
	if _, err := pub(`{"uri":"` + uriOf(root, "gone.go") + `","diagnostics":[]}`); !errors.Is(err, errGoplsUnknown) {
		t.Fatal(err)
	}
	if _, err := normalizeGoplsPublication(root, json.RawMessage(strings.Repeat(" ", maxGoplsParams+1))); !errors.Is(err, errGoplsInvalid) {
		t.Fatal(err)
	}
}

func TestNormalizeGoplsSymlinkEscapeIsOutside(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := goplsRoot(t, map[string]string{"a.go": "x"})
	if err := os.Symlink(filepath.Join(outside, "x.go"), filepath.Join(root.Project().Root, "link.go")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err := normalizeGoplsPublication(root, json.RawMessage(`{"uri":"`+uriOf(root, "link.go")+`","diagnostics":[]}`))
	if !errors.Is(err, errGoplsOutside) {
		t.Fatal(err)
	}
}

func TestGoWorkspaceDiscovery(t *testing.T) {
	mod := func(name string) string { return "module example.test/" + name + "\n\ngo 1.26\n" }
	cases := map[string]struct {
		files    map[string]string
		modules  []string
		partial  bool
		outside  bool
		wantNone bool
	}{
		"root module": {files: map[string]string{"go.mod": mod("a"), "go.sum": "", "a.go": "package a"}, modules: []string{""}},
		"root plus nested": {
			files:   map[string]string{"go.mod": mod("a"), "sub/go.mod": mod("a/sub"), "sub/deep/go.mod": mod("deep")},
			modules: []string{"", "sub", "sub/deep"},
		},
		"nested only, no parent promotion": {files: map[string]string{"sub/go.mod": mod("b")}, modules: []string{"sub"}},
		"go.work inside": {
			files:   map[string]string{"go.work": "go 1.26\n\nuse (\n\t./in\n)\n", "in/go.mod": mod("in")},
			modules: []string{"in"},
		},
		"go.work outside": {
			files:   map[string]string{"go.work": "go 1.26\n\nuse (\n\t./in\n\t../out\n)\n", "in/go.mod": mod("in")},
			modules: []string{"in"}, partial: true, outside: true,
		},
		"go.work absolute": {
			files:   map[string]string{"go.work": "go 1.26\n\nuse /tmp/elsewhere\n", "in/go.mod": mod("in")},
			modules: []string{"in"}, partial: true, outside: true,
		},
		"replace outside": {
			files:   map[string]string{"go.mod": mod("a") + "\nreplace example.test/x => ../x\n"},
			modules: []string{""}, partial: true, outside: true,
		},
		"replace inside": {
			files:   map[string]string{"go.mod": mod("a") + "\nreplace example.test/x => ./x\n", "x/go.mod": mod("x")},
			modules: []string{"", "x"},
		},
		"invalid go.mod":    {files: map[string]string{"go.mod": "not a module"}, partial: true, wantNone: true},
		"vendor and hidden": {files: map[string]string{"go.mod": mod("a"), "vendor/m/go.mod": mod("v"), ".hidden/go.mod": mod("h"), "testdata/go.mod": mod("t")}, modules: []string{""}, partial: true},
		"none":              {files: map[string]string{"readme.md": "x"}, wantNone: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, err := discoverGoWorkspace(goplsRoot(t, c.files))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.modules, c.modules) && (len(w.modules) != 0 || len(c.modules) != 0) {
				t.Fatalf("modules %v want %v", w.modules, c.modules)
			}
			if w.partial != c.partial || w.outside != c.outside {
				t.Fatalf("partial %v outside %v", w.partial, w.outside)
			}
			for _, p := range w.configs {
				if strings.Contains(p, "..") || filepath.IsAbs(p) {
					t.Fatalf("config escapes root: %s", p)
				}
			}
		})
	}
}

func TestGoWorkspaceLimitsAndSymlinks(t *testing.T) {
	files := map[string]string{}
	for i := range maxGoplsModules + 2 {
		files[fmt.Sprintf("m%02d/go.mod", i)] = "module example.test/m\n\ngo 1.26\n"
	}
	w, err := discoverGoWorkspace(goplsRoot(t, files))
	if err != nil || len(w.modules) != maxGoplsModules || !w.partial {
		t.Fatalf("%v %d %v", err, len(w.modules), w.partial)
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module example.test/o\n"), 0o600)
	root := goplsRoot(t, map[string]string{"go.mod": "module example.test/a\n\ngo 1.26\n"})
	if os.Symlink(outside, filepath.Join(root.Project().Root, "linked")) == nil {
		w, _ = discoverGoWorkspace(root)
		if !slices.Equal(w.modules, []string{""}) || !w.partial {
			t.Fatalf("symlink followed: %+v", w)
		}
	}
}

// goplsHost builds a reviewed fake host: a gopls binary and a Go toolchain.
func goplsHost(t *testing.T, version string, module string) (string, GoplsEnvironment) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(base, "bin")
	goroot := filepath.Join(base, "goroot")
	for _, p := range []string{bin, filepath.Join(goroot, "bin")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(bin, "gopls"), filepath.Join(goroot, "bin", "go")} {
		if err := os.WriteFile(p, []byte("synthetic"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, GoplsEnvironment{
		Home: base, Cache: base + "/c", ModuleCache: base + "/m", GoRoot: goroot,
		versionOf: func(string) (string, string, error) { return module, version, nil },
	}
}

func skipUnlessProduction(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("production platform gate")
	}
}

func TestDiscoverGoplsAvailability(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"go.mod": "module example.test/a\n\ngo 1.26\n", "a.go": "package a"})
	bin, env := goplsHost(t, "v0.24.0", goplsModulePath)

	p, err := DiscoverGopls(root, []string{bin}, env)
	if err != nil || p.Availability() != "available" || p.Version() != "v0.24.0" {
		t.Fatalf("%v %+v", err, p)
	}
	cfg, ok := p.Server()
	if !ok || !slices.Equal(cfg.Args, []string{"serve"}) || cfg.ProviderID != "gopls" || !slices.Contains(cfg.Env, "GOWORK=off") {
		t.Fatalf("%+v", cfg)
	}
	// Fixed argv and a CD-02 tool identity that gates the session.
	if err := validateCommand(root, []ToolInput{cfg.Tool()}, cfg.command()); err != nil {
		t.Fatal(err)
	}
	if st := p.Provider(); st.Coverage.Kind != "openedDocuments" || st.Coverage.ExternalChanges != "unwatched" || st.Coverage.Partial || !slices.Equal(st.Coverage.Roots, []string{""}) || !strings.Contains(st.Notice, "opened documents only") {
		t.Fatalf("%+v", st)
	}

	// Missing executable: independent status, no config, never a process.
	p, err = DiscoverGopls(root, []string{t.TempDir()}, env)
	if err != nil || p.Availability() != "missingTool" {
		t.Fatalf("%v %v", err, p.Availability())
	}
	if _, ok := p.Server(); ok {
		t.Fatal("server offered for a missing tool")
	}
	if p.Provider().Coverage.Kind != "openedDocuments" || !p.Provider().Coverage.Partial {
		t.Fatal("unavailable provider must not claim coverage")
	}
	// Relative PATH entries are ignored.
	if p, _ = DiscoverGopls(root, []string{"bin", "../bin"}, env); p.Availability() != "missingTool" {
		t.Fatal("relative path accepted")
	}
	// Any gopls version is accepted, as editors do; a foreign binary is not.
	for name, v := range map[string][2]string{"old": {goplsModulePath, "v0.1.0"}, "newer": {goplsModulePath, "v9.9.9"}, "devel": {goplsModulePath, "(devel)"}, "other": {"example.test/other", "v0.24.0"}} {
		bin, env := goplsHost(t, v[1], v[0])
		want := "available"
		if name == "other" {
			want = "unsupported"
		}
		if p, _ = DiscoverGopls(root, []string{bin}, env); p.Availability() != want {
			t.Errorf("%s: %s", name, p.Availability())
		}
	}
	_, env2 := goplsHost(t, "v0.24.0", goplsModulePath)
	env2.versionOf = func(string) (string, string, error) { return "", "", errors.New("not a go binary") }
	if p, _ = DiscoverGopls(root, []string{bin}, env2); p.Availability() != "unsupported" {
		t.Fatal("unreadable build info")
	}
	// Missing toolchain.
	env3 := env
	env3.GoRoot = t.TempDir()
	if p, _ = DiscoverGopls(root, []string{bin}, env3); p.Availability() != "missingTool" {
		t.Fatal("toolchain")
	}
	// No contained module and out-of-root workspace entries.
	empty := goplsRoot(t, map[string]string{"a.go": "package a"})
	if p, _ = DiscoverGopls(empty, []string{bin}, env); p.Availability() != "unsupported" {
		t.Fatal("no module")
	}
	work := goplsRoot(t, map[string]string{"go.work": "go 1.26\n\nuse ../out\n", "in/go.mod": "module example.test/in\n\ngo 1.26\n"})
	p, _ = DiscoverGopls(work, []string{bin}, env)
	if st := p.Provider(); p.Availability() != "available" || !st.Coverage.Partial || !strings.Contains(st.Notice, "outside the authorized project") || !slices.Equal(st.Coverage.Roots, []string{"in"}) {
		t.Fatalf("%+v", st)
	}
	if cfg, _ := p.Server(); !slices.Contains(cfg.Env, "GOWORK=off") {
		t.Fatal("go.work must stay disabled")
	}
	// Invalid host description is an error, not a status.
	env4 := env
	env4.Home = "relative"
	if _, err := DiscoverGopls(root, []string{bin}, env4); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
}

// bridgeEnv wires fake server -> SessionManager -> GoplsBridge -> Manager.
type bridgeEnv struct {
	*sessionEnv
	root    AuthorizedRoot
	cfg     ServerConfig
	h       *managerHarness
	srv     *fakeSrv
	notices chan string
}

func newBridgeEnv(t *testing.T, files map[string]string) *bridgeEnv {
	t.Helper()
	b := &bridgeEnv{notices: make(chan string, 64)}
	var bridge *GoplsBridge
	e := newEnv(t, SessionOptions{Notifications: func(n SessionNotification) { bridge.Handle(n) }})
	b.sessionEnv = e
	b.root, b.cfg = e.project()
	b.cfg.ProviderID = goplsProviderID // sessions already approved for "lsp"; re-approve
	e.approve(b.root, b.cfg)
	for name, body := range files {
		p := filepath.Join(b.root.Project().Root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.h = NewManagerForTest(t, b.root)
	bridge = NewGoplsBridge(b.root, b.h.Manager)
	bridge.Notice = func(s string) { b.notices <- s }
	docs := SessionDocuments{Sessions: e.mgr, Resolve: func(r AuthorizedRoot, path string) (DocumentServer, bool) {
		return DocumentServer{Config: b.cfg, LanguageID: "go"}, strings.HasSuffix(path, ".go")
	}}
	b.h.SetDocumentProvider(docs)
	if _, err := e.mgr.Attach(context.Background(), b.root, b.cfg); err != nil {
		t.Fatal(err)
	}
	b.srv = e.next()
	return b
}

func (b *bridgeEnv) push(rel string, version string, diags string) {
	v := ""
	if version != "" {
		v = `"version":` + version + `,`
	}
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"` + uriOf(b.root, rel) + `",` + v + `"diagnostics":[` + diags + `]}}`)
}

func (b *bridgeEnv) sets(t *testing.T) []ResultSet {
	t.Helper()
	s, err := b.h.GetSnapshot(b.h.snap.Context)
	if err != nil {
		t.Fatal(err)
	}
	return s.ResultSets
}

func (b *bridgeEnv) await(t *testing.T, what string, cond func([]ResultSet) bool) {
	t.Helper()
	eventually(t, what, func() bool { return cond(b.sets(t)) })
}

const errAt = `{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":2}},"severity":1,"code":"E","source":"compiler","message":"boom"}`

func TestGoplsBridgeFreshnessAndClear(t *testing.T) {
	b := newBridgeEnv(t, map[string]string{"a.go": "x", "b.go": "x"})
	ctx, c := context.Background(), b.h.snap.Context
	text := "package a\r\nvar 😀 = 1\r\n"
	if _, err := b.h.OpenDocument(ctx, c, "a.go", "d1", 1, text, ""); err != nil {
		t.Fatal(err)
	}
	// Matching version: current, range proven against CRLF + non-BMP text.
	b.push("a.go", "1", `{"range":{"start":{"line":1,"character":4},"end":{"line":1,"character":6}},"severity":1,"source":"compiler","code":"E","message":"emoji"}`)
	b.await(t, "published", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 1 })
	d := b.sets(t)[0].Diagnostics[0]
	if d.Freshness != "current" || d.Range == nil || d.Range.End.Character != 6 || d.Source != "gopls/compiler" || d.Basis.Kind != "document" || d.Basis.Version == nil || *d.Basis.Version != 1 {
		t.Fatalf("%+v", d)
	}
	// A column inside the surrogate pair is not provable: file-level, never positioned.
	b.push("a.go", "1", `{"range":{"start":{"line":1,"character":5},"end":{"line":1,"character":6}},"severity":2,"message":"split"}`)
	b.await(t, "invalid range", func(r []ResultSet) bool {
		return len(r[0].Diagnostics) == 1 && r[0].Diagnostics[0].LocationIssue == "invalidRange"
	})
	// Empty publication clears exactly this document's set.
	b.push("a.go", "1", ``)
	b.await(t, "cleared", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 0 })

	// After an edit, a late publication for the old version is rejected.
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 2, text+"x"); err != nil {
		t.Fatal(err)
	}
	b.push("a.go", "2", errAt)
	b.await(t, "v2", func(r []ResultSet) bool { return len(r[0].Diagnostics) == 1 })
	b.push("a.go", "1", ``) // late, would wrongly clear
	b.push("a.go", "7", ``) // future/wrong version
	b.push("a.go", "2", errAt+`,{"message":"second"}`)
	b.await(t, "late guarded", func(r []ResultSet) bool { return len(r[0].Diagnostics) == 2 })

	// Unversioned: uncertain and without a position; never marked current.
	b.push("a.go", "", errAt)
	b.await(t, "unversioned", func(r []ResultSet) bool {
		return len(r[0].Diagnostics) == 1 && r[0].Diagnostics[0].Freshness == "uncertain" && r[0].Diagnostics[0].Range == nil && r[0].Diagnostics[0].Basis.Version == nil
	})
	// Unopened/unknown files never create state.
	b.push("b.go", "1", errAt)
	// Out-of-root: provider notice, no result set.
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///etc/hosts","version":1,"diagnostics":[{"message":"leak"}]}}`)
	select {
	case n := <-b.notices:
		if n != OutsideRootNotice {
			t.Fatal(n)
		}
	case <-timeAfter():
		t.Fatal("no outside-root notice")
	}
	// Malformed payload: bounded partial notice.
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"http://x/a.go","diagnostics":[]}}`)
	select {
	case n := <-b.notices:
		if n != partialGoNotice {
			t.Fatal(n)
		}
	case <-timeAfter():
		t.Fatal("no partial notice")
	}
	if got := b.sets(t); len(got) != 1 || got[0].Scope.Path != "a.go" {
		t.Fatalf("%+v", got)
	}
	// Closing drops the set; a later push for the closed file is ignored.
	if _, err := b.h.CloseDocument(ctx, c, "d1"); err != nil {
		t.Fatal(err)
	}
	b.push("a.go", "2", errAt)
	b.push("b.go", "1", ``)
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"` + uriOf(b.root, "a.go") + `","diagnostics":[]}}`)
	eventually(t, "drained", func() bool { return b.mgr.Info(b.root, b.cfg).Phase == phaseInitialized })
	if got := b.sets(t); len(got) != 0 {
		t.Fatalf("closed document resurrected: %+v", got)
	}
}

func TestGoplsBridgeIgnoresOtherProvidersAndMethods(t *testing.T) {
	b := newBridgeEnv(t, map[string]string{"a.go": "x"})
	ctx, c := context.Background(), b.h.snap.Context
	_, _ = b.h.OpenDocument(ctx, c, "a.go", "d1", 1, "package a\n", "")
	bridge := NewGoplsBridge(b.root, b.h.Manager)
	for _, n := range []SessionNotification{
		{Project: b.root.Project(), ProviderID: "other", Method: "textDocument/publishDiagnostics"},
		{Project: Project{HostID: "x", Root: "/x"}, ProviderID: goplsProviderID, Method: "textDocument/publishDiagnostics"},
		{Project: b.root.Project(), ProviderID: goplsProviderID, Method: "window/showMessage"},
	} {
		bridge.Handle(n)
	}
	if got := b.sets(t); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestGoplsSessionKeepsWatchersRejectedAndStaysUp(t *testing.T) {
	b := newBridgeEnv(t, map[string]string{"a.go": "x"})
	// Reply bodies are covered by the CD-19 session tests; here the adapter's
	// session must stay up and keep reporting unwatched coverage.
	for i, m := range []string{"workspace/applyEdit", "workspace/executeCommand", "client/registerCapability"} {
		b.srv.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":{"edit":{},"registrations":[{"method":"workspace/didChangeWatchedFiles"}]}}`, 900+i, m))
	}
	eventually(t, "denied", func() bool { return b.mgr.Info(b.root, b.cfg).RejectedWatchers == 1 })
	if info := b.mgr.Info(b.root, b.cfg); info.ExternalChanges != "unwatched" || info.Phase != phaseInitialized {
		t.Fatalf("%+v", info)
	}
}

func TestGoplsCrashMarksSessionFailedAndKeepsOtherWork(t *testing.T) {
	b := newBridgeEnv(t, map[string]string{"a.go": "x"})
	ctx, c := context.Background(), b.h.snap.Context
	if _, err := b.h.OpenDocument(ctx, c, "a.go", "d1", 1, "package a\n", ""); err != nil {
		t.Fatal(err)
	}
	b.push("a.go", "1", errAt)
	b.await(t, "published", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 1 })
	_ = b.srv.Terminate() // server dies
	eventually(t, "failed", func() bool { return b.mgr.Info(b.root, b.cfg).Phase == phaseFailed })
	if info := b.mgr.Info(b.root, b.cfg); !info.RetryAvailable || errCode(info.Err) == "" {
		t.Fatalf("%+v", info)
	}
	// Edits to the open document now fail loudly and require a resync.
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 2, "package a\n// x\n"); err == nil {
		t.Fatal("change accepted by a dead session")
	}
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 3, "package a\n// y\n"); errCode(err) != "resyncRequired" {
		t.Fatal(err)
	}
}

func TestGoplsMissingExecutableLaunchFailure(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	e.fail = errors.New("no such file")
	_, err := e.mgr.Attach(context.Background(), root, cfg)
	if errCode(err) != "toolFailed" {
		t.Fatal(err)
	}
	if info := e.mgr.Info(root, cfg); info.Phase != phaseFailed || strings.Contains(fmt.Sprint(info.Err), "no such file") {
		t.Fatalf("%+v", info)
	}
}

func timeAfter() <-chan time.Time { return time.After(10 * time.Second) }
