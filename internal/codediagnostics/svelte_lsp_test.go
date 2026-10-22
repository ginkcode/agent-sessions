package codediagnostics

// SYNTHETIC fixtures: fake binaries, package trees and fake servers prove this
// adapter's handling only. No real Svelte language server was found or run, so
// no real-server behavior is claimed; see docs/plan/code-diagnostics-svelte.md
// and testdata/svelte-lsp/evidence.json ("none verified").

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

var syntheticSvelteVersion = SvelteLSVersion{"9.9.9-synthetic", "5.0.0-synthetic", "5.0.0-synthetic"}

func svelteHost(t *testing.T, name string, v SvelteLSVersion) ([]string, SvelteLSEnvironment) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	bin, node := filepath.Join(base, "bin"), filepath.Join(base, "node")
	for _, d := range []string{bin, node} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(bin, "svelteserver"), filepath.Join(node, "node")} {
		if err := os.WriteFile(p, []byte("synthetic"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return []string{bin}, SvelteLSEnvironment{
		Home: base, NodeDir: node,
		versionOf: func(string) (string, string, string, string, error) {
			return name, v.Server, v.Svelte, v.TypeScript, nil
		},
		supported: []SvelteLSVersion{syntheticSvelteVersion},
	}
}

func TestSvelteOwnershipPolicy(t *testing.T) {
	want := map[string][2]string{
		"a.svelte": {"svelte", "svelte"}, "dir/A.SVELTE": {"svelte", "svelte"},
		"a.ts": {"typescript", "typescript"}, "a.js": {"typescript", "javascript"}, "a.tsx": {"typescript", "typescriptreact"},
		"store.svelte.ts": {"typescript", "typescript"}, "store.svelte.js": {"typescript", "javascript"},
		"a.svelte.bak": {"", ""}, "a.go": {"", ""}, "svelte.config.js": {"typescript", "javascript"}, "tsconfig.json": {"", ""},
	}
	for f, w := range want {
		if p, l := DocumentOwner(f); p != w[0] || l != w[1] {
			t.Errorf("%s: %q %q", f, p, l)
		}
	}
}

func TestSveltePackageDetection(t *testing.T) {
	cases := map[string]struct {
		files    map[string]string
		packages []string
		partial  bool
	}{
		"config only, never evaluated":  {map[string]string{"svelte.config.js": "throw new Error('evaluated')", "a.svelte": ""}, []string{""}, false},
		"package.json dependency":       {map[string]string{"package.json": `{"devDependencies":{"svelte":"^5"}}`}, []string{""}, false},
		"kit dependency":                {map[string]string{"package.json": `{"dependencies":{"@sveltejs/kit":"^2"}}`}, []string{""}, false},
		"plain package":                 {map[string]string{"package.json": `{"dependencies":{"react":"1"}}`, "tsconfig.json": "{}"}, nil, false},
		"nested no promotion":           {map[string]string{"package.json": "{}", "web/svelte.config.js": "x"}, []string{"web"}, false},
		"malformed manifest":            {map[string]string{"package.json": "{not json"}, nil, false},
		"node_modules skipped":          {map[string]string{"svelte.config.js": "x", "node_modules/p/svelte.config.js": "x"}, []string{""}, false},
		"hidden skipped partial":        {map[string]string{"svelte.config.js": "x", ".x/svelte.config.js": "x"}, []string{""}, true},
		"monorepo":                      {map[string]string{"a/svelte.config.js": "x", "b/package.json": `{"dependencies":{"svelte":"5"}}`, "c/package.json": "{}"}, []string{"a", "b"}, false},
		"ts config alone is not svelte": {map[string]string{"tsconfig.json": "{}"}, nil, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, err := discoverSveltePackages(goplsRoot(t, c.files))
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

func TestSveltePackageLimitsAndSymlinks(t *testing.T) {
	files := map[string]string{}
	for i := range maxSveltePackages + 3 {
		files[filepath.Join("p"+string(rune('a'+i/26))+string(rune('a'+i%26)), "svelte.config.js")] = "x"
	}
	w, err := discoverSveltePackages(goplsRoot(t, files))
	if err != nil || len(w.packages) != maxSveltePackages || !w.partial {
		t.Fatalf("%v %d %v", err, len(w.packages), w.partial)
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "svelte.config.js"), []byte("x"), 0o600)
	root := goplsRoot(t, map[string]string{"svelte.config.js": "x"})
	if os.Symlink(outside, filepath.Join(root.Project().Root, "linked")) == nil {
		w, _ = discoverSveltePackages(root)
		if !slices.Equal(w.packages, []string{""}) || !w.partial {
			t.Fatalf("symlink followed: %+v", w)
		}
	}
}

func TestDiscoverSvelteLSAvailability(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"svelte.config.js": "x", "a.svelte": ""})
	path, env := svelteHost(t, svelteLSName, syntheticSvelteVersion)

	p, err := DiscoverSvelteLS(root, path, env)
	if err != nil || p.Availability() != "available" || p.ProjectLocal() || p.Version() != syntheticSvelteVersion {
		t.Fatalf("%v %+v", err, p)
	}
	cfg, ok := p.Server()
	if !ok || !slices.Equal(cfg.Args, []string{"--stdio"}) || cfg.ProviderID != svelteLSProviderID {
		t.Fatalf("%+v", cfg)
	}
	if err := validateCommand(root, []ToolInput{cfg.Tool()}, cfg.command()); err != nil {
		t.Fatal(err)
	}
	if st := p.Provider(); st.Coverage.Kind != "openedDocuments" || st.Coverage.ExternalChanges != "unwatched" || st.Coverage.Partial || !strings.Contains(st.Notice, "opened .svelte documents only") {
		t.Fatalf("%+v", st)
	}

	// The shipped verified list is empty: with no override, the same host is unsupported.
	real := env
	real.supported = nil
	if p, _ := DiscoverSvelteLS(root, path, real); p.Availability() != "unsupported" {
		t.Fatalf("an unverified server version was accepted: %s", p.Availability())
	}
	if len(supportedSvelteLSVersions) != 0 {
		t.Fatal("verified list must stay empty until a spike records evidence")
	}

	for name, tc := range map[string]struct {
		path  []string
		env   func(SvelteLSEnvironment) SvelteLSEnvironment
		state string
	}{
		"missing server": {[]string{t.TempDir()}, func(e SvelteLSEnvironment) SvelteLSEnvironment { return e }, "missingTool"},
		"relative path":  {[]string{"bin", "../bin"}, func(e SvelteLSEnvironment) SvelteLSEnvironment { return e }, "missingTool"},
		"missing node":   {path, func(e SvelteLSEnvironment) SvelteLSEnvironment { e.NodeDir = t.TempDir(); return e }, "missingTool"},
		"missing svelte": {path, func(e SvelteLSEnvironment) SvelteLSEnvironment {
			e.versionOf = func(string) (string, string, string, string, error) {
				return svelteLSName, "9.9.9-synthetic", "", "5.0.0-synthetic", nil
			}
			return e
		}, "missingTool"},
		"other version": {path, func(e SvelteLSEnvironment) SvelteLSEnvironment {
			e.versionOf = func(string) (string, string, string, string, error) {
				return svelteLSName, "1.0.0", "5.0.0-synthetic", "5.0.0-synthetic", nil
			}
			return e
		}, "unsupported"},
		"foreign package": {path, func(e SvelteLSEnvironment) SvelteLSEnvironment {
			e.versionOf = func(string) (string, string, string, string, error) {
				return "evil", "9.9.9-synthetic", "5.0.0-synthetic", "5.0.0-synthetic", nil
			}
			return e
		}, "unsupported"},
		"unreadable metadata": {path, func(e SvelteLSEnvironment) SvelteLSEnvironment {
			e.versionOf = func(string) (string, string, string, string, error) { return "", "", "", "", errors.New("no") }
			return e
		}, "unsupported"},
	} {
		p, err := DiscoverSvelteLS(root, tc.path, tc.env(env))
		if err != nil || p.Availability() != tc.state {
			t.Errorf("%s: %v %s", name, err, p.Availability())
			continue
		}
		if _, ok := p.Server(); ok || !p.Provider().Coverage.Partial {
			t.Errorf("%s: unavailable plan offers a server or full coverage", name)
		}
	}
	if p, _ = DiscoverSvelteLS(goplsRoot(t, map[string]string{"a.svelte": ""}), path, env); p.Availability() != "unsupported" {
		t.Fatal("no package")
	}
	bad := env
	bad.NodeDir = "relative"
	if _, err := DiscoverSvelteLS(root, path, bad); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
}

func TestSvelteProjectLocalResolutionAndMetadata(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"svelte.config.js": "x", "node_modules/svelte-language-server/bin/server.js": "x"})
	base := root.Project().Root
	entry := filepath.Join(base, "node_modules/svelte-language-server/bin/server.js")
	if err := os.Chmod(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(base, "node_modules/.bin"), 0o755)
	if os.Symlink("../svelte-language-server/bin/server.js", filepath.Join(base, "node_modules/.bin/svelteserver")) != nil {
		t.Skip("symlinks unavailable")
	}
	exe, local := resolveSvelteServer(root, nil)
	if !local || !strings.HasSuffix(exe, "bin/server.js") {
		t.Fatalf("%q %v", exe, local)
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "svelteserver"), []byte("x"), 0o755)
	root2 := goplsRoot(t, map[string]string{"svelte.config.js": "x"})
	_ = os.MkdirAll(filepath.Join(root2.Project().Root, "node_modules/.bin"), 0o755)
	_ = os.Symlink(filepath.Join(outside, "svelteserver"), filepath.Join(root2.Project().Root, "node_modules/.bin/svelteserver"))
	if exe, _ := resolveSvelteServer(root2, nil); exe != "" {
		t.Fatalf("escaped link resolved: %s", exe)
	}
	nm := filepath.Join(base, "node_modules")
	write := func(rel, body string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(nm, rel)), 0o755)
		_ = os.WriteFile(filepath.Join(nm, rel), []byte(body), 0o600)
	}
	write("svelte-language-server/package.json", `{"name":"svelte-language-server","version":"9.9.9-synthetic"}`)
	if n, s, sv, ts, err := svelteServerVersions(exe); err != nil || n != svelteLSName || s != "9.9.9-synthetic" || sv != "" || ts != "" {
		t.Fatal(n, s, sv, ts, err)
	}
	write("svelte/package.json", `{"name":"svelte","version":"5.0.0-synthetic"}`)
	write("typescript/package.json", `{"name":"not-typescript","version":"1"}`)
	if _, _, sv, ts, _ := svelteServerVersions(exe); sv != "5.0.0-synthetic" || ts != "" {
		t.Fatal(sv, ts)
	}
}

// Ownership across both providers: each file reaches exactly one provider and
// a package-less or unavailable provider resolves nothing.
func TestOwnedDocumentsMixedPackages(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{
		"package.json": `{"devDependencies":{"svelte":"5"}}`, "svelte.config.js": "x", "tsconfig.json": "{}",
		"src/App.svelte": "", "src/store.svelte.ts": "", "src/util.ts": "",
		"lib/package.json": "{}", "lib/x.ts": "", "lib/y.svelte": "",
		"other/z.ts": "", "other/z.svelte": "",
	})
	tsPath, tsEnv := tsHost(t, vtslsName, "0.3.0", "5.9.3")
	tsPlan, _ := DiscoverTypeScript(root, tsPath, tsEnv)
	svPath, svEnv := svelteHost(t, svelteLSName, syntheticSvelteVersion)
	svPlan, _ := DiscoverSvelteLS(root, svPath, svEnv)
	if tsPlan.Availability() != "available" || svPlan.Availability() != "available" {
		t.Fatal(tsPlan.Availability(), svPlan.Availability())
	}
	d := OwnedDocuments(&SessionManager{}, tsPlan, svPlan)
	for file, want := range map[string]string{
		"src/App.svelte": svelteLSProviderID, "src/store.svelte.ts": tsProviderID, "src/util.ts": tsProviderID,
		"lib/x.ts": tsProviderID, "other/z.ts": tsProviderID,
		"lib/y.svelte": svelteLSProviderID, "other/z.svelte": svelteLSProviderID,
		"src/a.go": "", "svelte.config.js": tsProviderID,
	} {
		s, ok := d.Resolve(root, file)
		if (want == "") == ok || (ok && s.Config.ProviderID != want) {
			t.Errorf("%s: want %q got %q %v", file, want, s.Config.ProviderID, ok)
		}
	}
	// Different sessions per provider, same key for every file of one provider:
	// package count never inflates sessions.
	a, _ := d.Resolve(root, "src/App.svelte")
	b, _ := d.Resolve(root, "lib/y.svelte")
	u, _ := d.Resolve(root, "src/util.ts")
	if keyOf(root, a.Config) != keyOf(root, b.Config) || keyOf(root, a.Config) == keyOf(root, u.Config) {
		t.Fatal("session keying is not (project, provider, policy)")
	}
	// Unavailable Svelte does not disturb TypeScript, and vice versa.
	un, _ := DiscoverSvelteLS(root, []string{t.TempDir()}, svEnv)
	d = OwnedDocuments(&SessionManager{}, tsPlan, un)
	if _, ok := d.Resolve(root, "src/App.svelte"); ok {
		t.Fatal("unavailable svelte resolved")
	}
	if _, ok := d.Resolve(root, "src/util.ts"); !ok {
		t.Fatal("typescript disturbed")
	}
	d = OwnedDocuments(&SessionManager{}, nil, svPlan)
	if _, ok := d.Resolve(root, "src/util.ts"); ok {
		t.Fatal("svelte-only plan claimed ts")
	}
	// Another project never resolves.
	if _, ok := d.Resolve(goplsRoot(t, map[string]string{"a.svelte": ""}), "a.svelte"); ok {
		t.Fatal("foreign project resolved")
	}
}

type svBridgeEnv struct{ *bridgeEnv }

func newSvelteBridgeEnv(t *testing.T, files map[string]string) *svBridgeEnv {
	t.Helper()
	b := &svBridgeEnv{&bridgeEnv{notices: make(chan string, 64)}}
	var bridge *SvelteBridge
	e := newEnv(t, SessionOptions{Notifications: func(n SessionNotification) { bridge.Handle(n) }})
	b.sessionEnv = e
	b.root, b.cfg = e.project()
	b.cfg.ProviderID = svelteLSProviderID
	e.approve(b.root, b.cfg)
	for name, body := range files {
		p := filepath.Join(b.root.Project().Root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.h = NewManagerForTest(t, b.root)
	bridge = NewSvelteBridge(b.root, b.h.Manager)
	bridge.Notice = func(s string) { b.notices <- s }
	b.h.SetDocumentProvider(SessionDocuments{Sessions: e.mgr, Resolve: func(r AuthorizedRoot, path string) (DocumentServer, bool) {
		owner, lang := DocumentOwner(path)
		return DocumentServer{Config: b.cfg, LanguageID: lang}, owner == svelteLSProviderID
	}})
	if _, err := e.mgr.Attach(context.Background(), b.root, b.cfg); err != nil {
		t.Fatal(err)
	}
	b.srv = e.next()
	return b
}

// Synthetic: versioned = current, unversioned = uncertain/no range, empty push
// clears only its own document, stale/future rejected, one didOpen per file.
func TestSvelteBridgeFreshnessSynthetic(t *testing.T) {
	b := newSvelteBridgeEnv(t, map[string]string{"A.svelte": "x", "B.svelte": "x", "u.ts": "x"})
	ctx, c := context.Background(), b.h.snap.Context
	text := "<script>\r\nlet s = \"😀\";\r\n</script>\r\n"
	for _, o := range []struct{ path, id string }{{"A.svelte", "d1"}, {"B.svelte", "d2"}} {
		if _, err := b.h.OpenDocument(ctx, c, o.path, o.id, 1, text, ""); err != nil {
			t.Fatal(err)
		}
	}
	// A TypeScript-owned file is refused by the Svelte provider: no second stream.
	if _, err := b.h.OpenDocument(ctx, c, "u.ts", "d3", 1, "x", ""); errCode(err) != "unsupported" {
		t.Fatalf("ts file reached the svelte provider: %v", err)
	}
	eventually(t, "single didOpen per document", func() bool {
		n := 0
		for _, m := range b.srv.seen() {
			if m == "textDocument/didOpen" {
				n++
			}
		}
		return n == 2
	})

	b.push("A.svelte", "1", errAt)
	b.push("B.svelte", "1", errAt)
	b.await(t, "published", func(r []ResultSet) bool {
		return len(r) == 2 && len(r[0].Diagnostics) == 1 && len(r[1].Diagnostics) == 1
	})
	d := b.sets(t)[0].Diagnostics[0]
	if d.Freshness != "current" || d.Source != "svelte/compiler" || d.Range == nil || d.Basis.Version == nil || *d.Basis.Version != 1 {
		t.Fatalf("%+v", d)
	}
	b.push("A.svelte", "1", ``)
	b.await(t, "only A cleared", func(r []ResultSet) bool {
		a, bb := 0, 0
		for _, s := range r {
			if s.Scope.Path == "A.svelte" {
				a = len(s.Diagnostics)
			} else {
				bb = len(s.Diagnostics)
			}
		}
		return len(r) == 2 && a == 0 && bb == 1
	})

	if _, err := b.h.ChangeDocument(ctx, c, "d1", 2, text+"x"); err != nil {
		t.Fatal(err)
	}
	b.push("A.svelte", "1", errAt) // stale
	b.push("A.svelte", "9", errAt) // future
	b.push("A.svelte", "2", errAt+`,{"message":"second"}`)
	b.await(t, "stale guarded", func(r []ResultSet) bool {
		for _, s := range r {
			if s.Scope.Path == "A.svelte" {
				return len(s.Diagnostics) == 2
			}
		}
		return false
	})
	b.push("A.svelte", "", errAt)
	b.await(t, "unversioned uncertain", func(r []ResultSet) bool {
		for _, s := range r {
			if s.Scope.Path == "A.svelte" {
				return len(s.Diagnostics) == 1 && s.Diagnostics[0].Freshness == "uncertain" && s.Diagnostics[0].Range == nil && s.Diagnostics[0].Basis.Version == nil
			}
		}
		return false
	})

	// TypeScript-owned/config files never enter the Svelte set.
	b.push("u.ts", "1", errAt)
	b.push("never-opened.svelte", "1", errAt)
	// Virtual documents are not mapped: a bounded limited-coverage notice only.
	for _, uri := range []string{"svelte-virtual:///A.svelte.tsx", "file:///etc/hosts"} {
		b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"` + uri + `","version":1,"diagnostics":[{"message":"leak","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}]}}`)
	}
	if n := <-b.notices; n != SvelteVirtualNotice {
		t.Fatal(n)
	}
	if n := <-b.notices; n != OutsideRootNotice {
		t.Fatal(n)
	}
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"x","diagnostics":null}}`)
	if n := <-b.notices; n != partialSvelteNotice {
		t.Fatal(n)
	}
	if got := b.sets(t); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	_ = b.srv.Terminate()
	eventually(t, "failed", func() bool { return b.mgr.Info(b.root, b.cfg).Phase == phaseFailed })
}

func TestSvelteBridgeIgnoresOtherProviders(t *testing.T) {
	b := newSvelteBridgeEnv(t, map[string]string{"A.svelte": "x"})
	_, _ = b.h.OpenDocument(context.Background(), b.h.snap.Context, "A.svelte", "d1", 1, "x", "")
	bridge := NewSvelteBridge(b.root, b.h.Manager)
	for _, n := range []SessionNotification{
		{Project: b.root.Project(), ProviderID: tsProviderID, Method: "textDocument/publishDiagnostics"},
		{Project: Project{HostID: "x", Root: "/x"}, ProviderID: svelteLSProviderID, Method: "textDocument/publishDiagnostics"},
		{Project: b.root.Project(), ProviderID: svelteLSProviderID, Method: "window/showMessage"},
	} {
		bridge.Handle(n)
	}
	if got := b.sets(t); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestSvelteEvidenceRecordsNoneVerified(t *testing.T) {
	data, err := os.ReadFile("testdata/svelte-lsp/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Result            string
		SupportedVersions []any
	}
	if err := json.Unmarshal(data, &ev); err != nil || ev.Result != "none verified" || len(ev.SupportedVersions) != 0 {
		t.Fatalf("%v %+v", err, ev)
	}
}
