package codediagnostics

// SYNTHETIC fixtures: the fake servers, binaries and package trees below prove
// this adapter's handling only. They do not claim real-server behavior. Observed
// vtsls behavior is recorded (and replayed through the normalizer) from
// testdata/typescript/evidence.json; see docs/plan/code-diagnostics-typescript.md.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func tsHost(t *testing.T, name, wrapper, typescript string) ([]string, TSEnvironment) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	bin, node := filepath.Join(base, "bin"), filepath.Join(base, "node")
	for _, d := range []string{bin, node} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(bin, "vtsls"), filepath.Join(node, "node")} {
		if err := os.WriteFile(p, []byte("synthetic"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return []string{bin}, TSEnvironment{
		Home: base, NodeDir: node,
		versionOf: func(string) (string, string, string, error) { return name, wrapper, typescript, nil },
	}
}

func TestTSLanguageOwnership(t *testing.T) {
	want := map[string]string{
		"a.ts": "typescript", "a.mts": "typescript", "a.cts": "typescript", "a.tsx": "typescriptreact",
		"a.js": "javascript", "a.mjs": "javascript", "a.cjs": "javascript", "a.jsx": "javascriptreact",
		"dir/A.TS": "typescript",
	}
	for f, lang := range want {
		if got, ok := TSLanguageID(f); !ok || got != lang {
			t.Errorf("%s: %q %v", f, got, ok)
		}
	}
	for _, f := range []string{"a.svelte", "a.go", "a.json", "a.d.ts.map", "ts", "a.ts.bak", "tsconfig.json"} {
		if _, ok := TSLanguageID(f); ok {
			t.Errorf("%s must not be owned", f)
		}
	}
}

func TestTSPackageDiscovery(t *testing.T) {
	cases := map[string]struct {
		files    map[string]string
		packages []string
		partial  bool
	}{
		"root package":         {map[string]string{"package.json": "{}", "a.ts": ""}, []string{""}, false},
		"tsconfig only":        {map[string]string{"tsconfig.json": "{}"}, []string{""}, false},
		"jsconfig only":        {map[string]string{"jsconfig.json": "{}"}, []string{""}, false},
		"nested no promotion":  {map[string]string{"web/package.json": "{}", "web/src/a.ts": ""}, []string{"web"}, false},
		"monorepo":             {map[string]string{"package.json": "{}", "packages/a/package.json": "{}", "packages/b/tsconfig.json": "{}"}, []string{"", "packages/a", "packages/b"}, false},
		"node_modules skipped": {map[string]string{"package.json": "{}", "node_modules/x/package.json": "{}"}, []string{""}, false},
		"hidden skipped":       {map[string]string{"package.json": "{}", ".cache/package.json": "{}"}, []string{""}, true},
		"none":                 {map[string]string{"a.ts": ""}, nil, false},
		"extends is not read":  {map[string]string{"tsconfig.json": `{"extends":"../../outside/tsconfig.json","references":[{"path":"../x"}]}`}, []string{""}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, err := discoverTSPackages(goplsRoot(t, c.files))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.packages, c.packages) && (len(w.packages) != 0 || len(c.packages) != 0) {
				t.Fatalf("packages %v want %v", w.packages, c.packages)
			}
			if w.partial != c.partial {
				t.Fatalf("partial %v", w.partial)
			}
			for _, p := range w.configs {
				if strings.Contains(p, "..") || filepath.IsAbs(p) || strings.Contains(p, "node_modules") {
					t.Fatalf("bad config path %s", p)
				}
			}
		})
	}
}

func TestTSPackageLimitsAndSymlinks(t *testing.T) {
	files := map[string]string{}
	for i := range maxTSPackages + 3 {
		files[filepath.Join("p"+string(rune('a'+i/26))+string(rune('a'+i%26)), "package.json")] = "{}"
	}
	w, err := discoverTSPackages(goplsRoot(t, files))
	if err != nil || len(w.packages) != maxTSPackages || !w.partial {
		t.Fatalf("%v %d %v", err, len(w.packages), w.partial)
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "package.json"), []byte("{}"), 0o600)
	root := goplsRoot(t, map[string]string{"package.json": "{}"})
	if os.Symlink(outside, filepath.Join(root.Project().Root, "linked")) == nil {
		w, _ = discoverTSPackages(root)
		if !slices.Equal(w.packages, []string{""}) || !w.partial {
			t.Fatalf("symlink followed: %+v", w)
		}
	}
}

func TestDiscoverTypeScriptAvailability(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"package.json": "{}", "a.ts": ""})
	path, env := tsHost(t, vtslsName, "0.3.0", "5.9.3")

	p, err := DiscoverTypeScript(root, path, env)
	if err != nil || p.Availability() != "available" || p.ProjectLocal() {
		t.Fatalf("%v %+v", err, p)
	}
	if w, ts := p.Version(); w != "0.3.0" || ts != "5.9.3" {
		t.Fatal(w, ts)
	}
	cfg, ok := p.Server()
	if !ok || !slices.Equal(cfg.Args, []string{"--stdio"}) || cfg.ProviderID != tsProviderID {
		t.Fatalf("%+v", cfg)
	}
	if err := validateCommand(root, []ToolInput{cfg.Tool()}, cfg.command()); err != nil {
		t.Fatal(err)
	}
	if st := p.Provider(); st.Coverage.Kind != "openedDocuments" || st.Coverage.ExternalChanges != "unwatched" || st.Coverage.Partial || !strings.Contains(st.Notice, "opened documents only") {
		t.Fatalf("%+v", st)
	}

	// Independent unavailability: never a server, never a process.
	for name, tc := range map[string]struct {
		path  []string
		env   func(TSEnvironment) TSEnvironment
		state string
	}{
		"missing wrapper": {[]string{t.TempDir()}, func(e TSEnvironment) TSEnvironment { return e }, "missingTool"},
		"relative path":   {[]string{"bin", "../bin"}, func(e TSEnvironment) TSEnvironment { return e }, "missingTool"},
		"missing node":    {path, func(e TSEnvironment) TSEnvironment { e.NodeDir = t.TempDir(); return e }, "missingTool"},
		"missing typescript": {path, func(e TSEnvironment) TSEnvironment {
			e.versionOf = func(string) (string, string, string, error) { return vtslsName, "0.3.0", "", nil }
			return e
		}, "missingTool"},
		"old wrapper": {path, func(e TSEnvironment) TSEnvironment {
			e.versionOf = func(string) (string, string, string, error) { return vtslsName, "0.1.0", "5.9.3", nil }
			return e
		}, "unsupported"},
		"other typescript": {path, func(e TSEnvironment) TSEnvironment {
			e.versionOf = func(string) (string, string, string, error) { return vtslsName, "0.3.0", "6.0.0", nil }
			return e
		}, "unsupported"},
		"foreign package": {path, func(e TSEnvironment) TSEnvironment {
			e.versionOf = func(string) (string, string, string, error) { return "evil", "0.3.0", "5.9.3", nil }
			return e
		}, "unsupported"},
		"unreadable metadata": {path, func(e TSEnvironment) TSEnvironment {
			e.versionOf = func(string) (string, string, string, error) { return "", "", "", errors.New("no") }
			return e
		}, "unsupported"},
	} {
		p, err := DiscoverTypeScript(root, tc.path, tc.env(env))
		if err != nil || p.Availability() != tc.state {
			t.Errorf("%s: %v %s", name, err, p.Availability())
			continue
		}
		if _, ok := p.Server(); ok || !p.Provider().Coverage.Partial {
			t.Errorf("%s: unavailable plan offers a server or full coverage", name)
		}
	}
	// No contained package: unsupported, never promoted to a parent.
	if p, _ = DiscoverTypeScript(goplsRoot(t, map[string]string{"a.ts": ""}), path, env); p.Availability() != "unsupported" {
		t.Fatal("no package")
	}
	bad := env
	bad.NodeDir = "relative"
	if _, err := DiscoverTypeScript(root, path, bad); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
}

func TestTSProjectLocalBinaryResolution(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{
		"package.json": "{}",
		"node_modules/@vtsls/language-server/bin/vtsls.js": "x",
	})
	base := root.Project().Root
	if err := os.Chmod(filepath.Join(base, "node_modules/@vtsls/language-server/bin/vtsls.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "node_modules/.bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../@vtsls/language-server/bin/vtsls.js", filepath.Join(base, "node_modules/.bin/vtsls")); err != nil {
		t.Skip("symlinks unavailable")
	}
	exe, local := resolveVtsls(root, nil)
	if !local || !strings.HasSuffix(exe, "bin/vtsls.js") {
		t.Fatalf("%q %v", exe, local)
	}
	// A project-local link that escapes the root is ignored.
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "vtsls"), []byte("x"), 0o755)
	root2 := goplsRoot(t, map[string]string{"package.json": "{}"})
	_ = os.MkdirAll(filepath.Join(root2.Project().Root, "node_modules/.bin"), 0o755)
	_ = os.Symlink(filepath.Join(outside, "vtsls"), filepath.Join(root2.Project().Root, "node_modules/.bin/vtsls"))
	if exe, _ := resolveVtsls(root2, nil); exe != "" {
		t.Fatalf("escaped link resolved: %s", exe)
	}
	// Package metadata is read, not executed; names/versions come from package.json files.
	nm := filepath.Join(base, "node_modules")
	_ = os.WriteFile(filepath.Join(nm, "@vtsls/language-server/package.json"), []byte(`{"name":"@vtsls/language-server","version":"0.3.0"}`), 0o600)
	if name, w, ts, err := vtslsVersions(exe); err != nil || name != vtslsName || w != "0.3.0" || ts != "" {
		t.Fatalf("%q %q %q %v", name, w, ts, err)
	}
	_ = os.MkdirAll(filepath.Join(nm, "typescript"), 0o755)
	_ = os.WriteFile(filepath.Join(nm, "typescript/package.json"), []byte(`{"name":"typescript","version":"5.9.3"}`), 0o600)
	if _, _, ts, err := vtslsVersions(exe); err != nil || ts != "5.9.3" {
		t.Fatal(ts, err)
	}
}

func TestTSDocumentResolution(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"web/package.json": "{}", "web/src/a.ts": "", "other/b.ts": "", "c.ts": ""})
	path, env := tsHost(t, vtslsName, "0.3.0", "5.9.3")
	p, _ := DiscoverTypeScript(root, path, env)
	d := p.Documents(&SessionManager{})
	for file, want := range map[string]bool{
		"web/src/a.ts": true, "web/x.jsx": true,
		"other/b.ts": false, "c.ts": false, // no parent promotion: root has no package
		"web/a.svelte": false, "web/a.go": false,
	} {
		if _, ok := d.Resolve(root, file); ok != want {
			t.Errorf("%s: %v", file, ok)
		}
	}
	if s, _ := d.Resolve(root, "web/src/a.ts"); s.LanguageID != "typescript" || s.Config.ProviderID != tsProviderID {
		t.Fatalf("%+v", s)
	}
	// An unavailable plan resolves nothing.
	un, _ := DiscoverTypeScript(root, []string{t.TempDir()}, env)
	if _, ok := un.Documents(&SessionManager{}).Resolve(root, "web/src/a.ts"); ok {
		t.Fatal("unavailable plan resolved a document")
	}
}

// TestTSEvidenceReplay replays recorded vtsls publications through the shared
// normalizer. It proves normalization of observed payloads only.
func TestTSEvidenceReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/typescript/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Versions   map[string]string
		Initialize struct {
			Capabilities struct{ TextDocumentSync map[string]any }
		}
		Scenarios []struct {
			ID           string
			Publications []struct{ Params json.RawMessage }
		}
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(supportedVtslsVersions, struct{ Wrapper, TypeScript string }{ev.Versions["@vtsls/language-server"], ev.Versions["typescript"]}) {
		t.Fatalf("evidence versions %v are not in the supported list", ev.Versions)
	}
	if ev.Initialize.Capabilities.TextDocumentSync["change"] != float64(2) {
		t.Fatalf("%v", ev.Initialize.Capabilities.TextDocumentSync)
	}
	root := goplsRoot(t, map[string]string{"a.ts": "x", "tsconfig.json": "{}"})
	n := 0
	for _, s := range ev.Scenarios {
		for _, p := range s.Publications {
			fixed := strings.ReplaceAll(string(p.Params), "file:///fixture/", uriOf(root, "")+"/")
			pub, err := normalizeLSPPublication(root, json.RawMessage(fixed), tsProviderID)
			if err != nil {
				t.Fatalf("%s: %v", s.ID, err)
			}
			if pub.Version != nil {
				t.Fatalf("%s: observed vtsls pushes are unversioned", s.ID)
			}
			for _, d := range pub.Diagnostics {
				if d.Severity != "error" || d.Source != "typescript/ts" || d.Code != "2322" || d.Range == nil || d.Range.Start.Character != 22 {
					t.Fatalf("%+v", d)
				}
			}
			n++
		}
	}
	if n != 5 {
		t.Fatal(n)
	}
}

type tsBridgeEnv struct{ *bridgeEnv }

func newTSBridgeEnv(t *testing.T, files map[string]string) *tsBridgeEnv {
	t.Helper()
	b := &tsBridgeEnv{&bridgeEnv{notices: make(chan string, 64)}}
	var bridge *TSBridge
	e := newEnv(t, SessionOptions{Notifications: func(n SessionNotification) { bridge.Handle(n) }})
	b.sessionEnv = e
	b.root, b.cfg = e.project()
	b.cfg.ProviderID = tsProviderID
	e.approve(b.root, b.cfg)
	for name, body := range files {
		p := filepath.Join(b.root.Project().Root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.h = NewManagerForTest(t, b.root)
	bridge = NewTSBridge(b.root, b.h.Manager)
	bridge.Notice = func(s string) { b.notices <- s }
	b.h.SetDocumentProvider(SessionDocuments{Sessions: e.mgr, Resolve: func(r AuthorizedRoot, path string) (DocumentServer, bool) {
		lang, ok := TSLanguageID(path)
		return DocumentServer{Config: b.cfg, LanguageID: lang}, ok
	}})
	if _, err := e.mgr.Attach(context.Background(), b.root, b.cfg); err != nil {
		t.Fatal(err)
	}
	b.srv = e.next()
	return b
}

// Synthetic fake server: versioned pushes are accepted, unversioned ones are
// uncertain, mismatched ones are rejected, empty pushes clear.
func TestTSBridgeFreshnessSynthetic(t *testing.T) {
	b := newTSBridgeEnv(t, map[string]string{"a.ts": "x", "tsconfig.json": "{}", "w.svelte": "x"})
	ctx, c := context.Background(), b.h.snap.Context
	text := "const s = \"😀\";\r\nlet n: number = s;\r\n"
	if _, err := b.h.OpenDocument(ctx, c, "a.ts", "d1", 1, text, ""); err != nil {
		t.Fatal(err)
	}
	b.push("a.ts", "1", `{"range":{"start":{"line":1,"character":4},"end":{"line":1,"character":5}},"severity":1,"code":2322,"source":"ts","message":"bad"}`)
	b.await(t, "published", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 1 })
	d := b.sets(t)[0].Diagnostics[0]
	if d.Freshness != "current" || d.Source != "typescript/ts" || d.Code != "2322" || d.Range == nil || d.Basis.Version == nil || *d.Basis.Version != 1 {
		t.Fatalf("%+v", d)
	}
	b.push("a.ts", "1", ``)
	b.await(t, "cleared", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 0 })

	if _, err := b.h.ChangeDocument(ctx, c, "d1", 2, text+"x"); err != nil {
		t.Fatal(err)
	}
	b.push("a.ts", "2", errAt)
	b.await(t, "v2", func(r []ResultSet) bool { return len(r[0].Diagnostics) == 1 })
	b.push("a.ts", "1", ``) // stale
	b.push("a.ts", "9", ``) // future
	b.push("a.ts", "2", errAt+`,{"message":"second"}`)
	b.await(t, "stale guarded", func(r []ResultSet) bool { return len(r[0].Diagnostics) == 2 })

	// Unversioned (what the observed vtsls does): uncertain, never positioned.
	b.push("a.ts", "", errAt)
	b.await(t, "unversioned", func(r []ResultSet) bool {
		return len(r[0].Diagnostics) == 1 && r[0].Diagnostics[0].Freshness == "uncertain" && r[0].Diagnostics[0].Range == nil && r[0].Diagnostics[0].Basis.Version == nil
	})
	// Config files and unowned/unopened files never create state.
	b.push("tsconfig.json", "", errAt)
	b.push("w.svelte", "1", errAt)
	b.push("b.ts", "1", errAt)
	// Out-of-root and malformed publications are bounded provider notices.
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///etc/hosts","version":1,"diagnostics":[{"message":"leak"}]}}`)
	if n := <-b.notices; n != OutsideRootNotice {
		t.Fatal(n)
	}
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"http://x/a.ts","diagnostics":[]}}`)
	if n := <-b.notices; n != partialTSNotice {
		t.Fatal(n)
	}
	if got := b.sets(t); len(got) != 1 || got[0].Scope.Path != "a.ts" {
		t.Fatalf("%+v", got)
	}
	// A crash fails loudly and requires resync.
	_ = b.srv.Terminate()
	eventually(t, "failed", func() bool { return b.mgr.Info(b.root, b.cfg).Phase == phaseFailed })
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 3, "x"); err == nil {
		t.Fatal("change accepted by a dead session")
	}
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 4, "y"); errCode(err) != "resyncRequired" {
		t.Fatal(err)
	}
}

func TestTSBridgeIgnoresOtherProviders(t *testing.T) {
	b := newTSBridgeEnv(t, map[string]string{"a.ts": "x"})
	_, _ = b.h.OpenDocument(context.Background(), b.h.snap.Context, "a.ts", "d1", 1, "x", "")
	bridge := NewTSBridge(b.root, b.h.Manager)
	for _, n := range []SessionNotification{
		{Project: b.root.Project(), ProviderID: goplsProviderID, Method: "textDocument/publishDiagnostics"},
		{Project: Project{HostID: "x", Root: "/x"}, ProviderID: tsProviderID, Method: "textDocument/publishDiagnostics"},
		{Project: b.root.Project(), ProviderID: tsProviderID, Method: "window/showMessage"},
	} {
		bridge.Handle(n)
	}
	if got := b.sets(t); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
