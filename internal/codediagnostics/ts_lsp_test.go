package codediagnostics

// SYNTHETIC fixtures: the fake servers, binaries and package trees below prove
// this adapter's handling only. They do not claim real-server behavior. Observed
// vtsls behavior is recorded (and replayed through the normalizer) from
// testdata/typescript/evidence.json; see docs/plan/code-diagnostics-typescript.md.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTree writes SYNTHETIC files below base; package.json bodies are metadata,
// .js bodies are never loaded.
func writeTree(t *testing.T, base string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func pkgJSON(name, version string) string {
	data, _ := json.Marshal(map[string]string{"name": name, "version": version})
	return string(data)
}

// writeVtsls lays out a vtsls package at server with its dependencies in deps
// (nested for a global npm install, the shared node_modules when flattened).
func writeVtsls(t *testing.T, server, deps, name, wrapper, service, typescript string) {
	t.Helper()
	writeTree(t, server, map[string]string{"package.json": pkgJSON(name, wrapper), "bin/vtsls.js": "#!/usr/bin/env node\nrequire(\"../dist/main.js\");\n", "dist/main.js": "synthetic"})
	writeTree(t, deps, map[string]string{
		"@vtsls/language-service/package.json": pkgJSON(vtslsServiceName, service), "@vtsls/language-service/dist/index.js": "synthetic",
		"typescript/package.json": pkgJSON("typescript", typescript), "typescript/lib/tsserver.js": "synthetic",
		"typescript/lib/_tsserver.js": "synthetic", "typescript/lib/typescript.js": "synthetic",
	})
}

// writeNode creates a fake (never executed) node binary under prefix/bin and
// the installed header that carries its version.
func writeNode(t *testing.T, prefix, version string) {
	t.Helper()
	v := strings.Split(version, ".")
	writeTree(t, prefix, map[string]string{"include/node/node_version.h": "#ifndef SRC_NODE_VERSION_H_\n#define NODE_MAJOR_VERSION " + v[0] + "\n#define NODE_MINOR_VERSION " + v[1] + "\n#define NODE_PATCH_VERSION " + v[2] + "\n#define NODE_VERSION_IS_RELEASE 1\n"})
	writeTree(t, prefix, map[string]string{"bin/node": "\xcf\xfa\xed\xfe synthetic"})
	if err := os.Chmod(filepath.Join(prefix, "bin", "node"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// tsHost builds a SYNTHETIC global npm install: <prefix>/bin/vtsls links to
// <prefix>/lib/node_modules/@vtsls/language-server/bin/vtsls.js with nested
// dependencies, beside <prefix>/bin/node, at the verified versions. Nothing in
// it is ever executed.
func tsHost(t *testing.T) ([]string, TSEnvironment) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	prefix := filepath.Join(base, "prefix")
	server := filepath.Join(prefix, "lib", "node_modules", "@vtsls", "language-server")
	writeVtsls(t, server, filepath.Join(server, "node_modules"), vtslsName, "0.3.0", "0.3.0", "5.9.3")
	writeNode(t, prefix, "22.23.2")
	if err := os.Symlink("../lib/node_modules/@vtsls/language-server/bin/vtsls.js", filepath.Join(prefix, "bin", "vtsls")); err != nil {
		t.Fatal(err)
	}
	return []string{filepath.Join(prefix, "bin")}, TSEnvironment{Home: base}
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
	path, env := tsHost(t)
	prefix := filepath.Dir(path[0])
	server := filepath.Join(prefix, "lib/node_modules/@vtsls/language-server")
	deps := filepath.Join(server, "node_modules")

	p, err := DiscoverTypeScript(root, path, env)
	if err != nil || p.Availability() != "available" || p.ProjectLocal() {
		t.Fatalf("%v %+v", err, p)
	}
	if w, ts := p.Version(); w != "0.3.0" || ts != "5.9.3" || p.NodeVersion() != "22.23.2" {
		t.Fatal(w, ts, p.NodeVersion())
	}
	cfg, ok := p.Server()
	// node runs the package's own entry: no shell, npx or bin shim in argv.
	if !ok || cfg.ProviderID != tsProviderID || cfg.Executable != filepath.Join(prefix, "bin", "node") ||
		!slices.Equal(cfg.Args, []string{filepath.Join(server, "bin", "vtsls.js"), "--stdio"}) {
		t.Fatalf("%+v", cfg)
	}
	if want := []string{"HOME=" + env.Home, "PATH=" + filepath.Join(prefix, "bin") + ":/usr/bin:/bin", "NODE_DISABLE_COMPILE_CACHE=1"}; !slices.Equal(cfg.Env, want) {
		t.Fatalf("env %v", cfg.Env)
	}
	if string(cfg.Settings) != string(tsSettings("")) || !strings.Contains(cfg.Policy(), "ata-off") || !strings.Contains(cfg.Policy(), `\"disableAutomaticTypeAcquisition\":true`) {
		t.Fatalf("policy %s", cfg.Policy())
	}
	ids, files := []string{}, []string{}
	for _, tool := range cfg.Toolchain {
		ids, files = append(ids, tool.ProviderID), append(files, tool.Executable)
	}
	wantFiles := []string{
		filepath.Join(server, "bin/vtsls.js"), filepath.Join(server, "dist/main.js"), filepath.Join(deps, "@vtsls/language-service/dist/index.js"),
		filepath.Join(deps, "typescript/lib/tsserver.js"), filepath.Join(deps, "typescript/lib/_tsserver.js"), filepath.Join(deps, "typescript/lib/typescript.js"),
	}
	if len(ids) != 6 || ids[0] != "typescript-toolchain-vtsls-entry" || !slices.Equal(files, wantFiles) {
		t.Fatalf("toolchain %v %v", ids, files)
	}
	if err := validateCommand(root, cfg.Tools(), cfg.command()); err != nil {
		t.Fatal(err)
	}
	st := p.Provider()
	if st.Coverage.Kind != "openedDocuments" || st.Coverage.ExternalChanges != "unwatched" || st.Coverage.Partial ||
		!strings.Contains(st.Notice, "opened documents only") || !strings.Contains(st.Notice, "uncertain") {
		t.Fatalf("%+v", st)
	}
	// A loaded TypeScript file changing changes the composite fingerprint.
	before, _ := Fingerprint(root, cfg.Tools())
	writeTree(t, deps, map[string]string{"typescript/lib/typescript.js": "changed"})
	if after, err := Fingerprint(root, cfg.Tools()); err != nil || after == before {
		t.Fatal("typescript change kept the fingerprint", err)
	}

	// Independent unavailability: never a server, never a process.
	host := func(mutate func(prefix, server, deps string)) []string {
		path, _ := tsHost(t)
		prefix := filepath.Dir(path[0])
		server := filepath.Join(prefix, "lib/node_modules/@vtsls/language-server")
		mutate(prefix, server, filepath.Join(server, "node_modules"))
		return path
	}
	shim := t.TempDir()
	writeTree(t, shim, map[string]string{"mise": "\xcf\xfa\xed\xfe launcher", "node": "#!/bin/sh\nexec mise x -- node \"$@\"\n"})
	_ = os.Chmod(filepath.Join(shim, "mise"), 0o755)
	_ = os.Chmod(filepath.Join(shim, "node"), 0o755)
	_ = os.Symlink("mise", filepath.Join(shim, "vtsls"))
	oldNode, noHeader := t.TempDir(), t.TempDir()
	writeNode(t, oldNode, "16.20.2")
	writeNode(t, noHeader, "22.0.0")
	_ = os.RemoveAll(filepath.Join(noHeader, "include"))
	for name, tc := range map[string]struct {
		path   []string
		node   string
		state  string
		notice string
	}{
		"missing wrapper":      {[]string{t.TempDir()}, "", "missingTool", "npm install -g @vtsls/language-server"},
		"relative path":        {[]string{"bin", "../bin"}, "", "missingTool", "The app downloads vtsls"},
		"version-manager shim": {[]string{shim}, "", "unsupported", "version-manager shim"},
		"shim before install":  {append([]string{shim}, path...), "", "available", ""},
		"missing node":         {host(func(p, _, _ string) { _ = os.Remove(filepath.Join(p, "bin", "node")) }), "", "missingTool", "Node.js 18 or newer"},
		"node shim only":       {path, shim, "missingTool", "Node.js 18 or newer"},
		"old node":             {path, filepath.Join(oldNode, "bin"), "unsupported", "Node.js 16.20.2 is too old"},
		"unverified node":      {path, filepath.Join(noHeader, "bin"), "unsupported", "could not be verified"},
		"missing typescript":   {host(func(_, _, d string) { _ = os.RemoveAll(filepath.Join(d, "typescript")) }), "", "missingTool", "TypeScript package"},
		"missing service":      {host(func(_, _, d string) { _ = os.RemoveAll(filepath.Join(d, "@vtsls")) }), "", "missingTool", "language-service"},
		// Any consistent vtsls and TypeScript version is used, as editors do.
		"old wrapper": {host(func(_, s, d string) {
			writeTree(t, s, map[string]string{"package.json": pkgJSON(vtslsName, "0.1.0")})
			writeTree(t, d, map[string]string{"@vtsls/language-service/package.json": pkgJSON(vtslsServiceName, "0.1.0")})
		}), "", "available", ""},
		"other typescript": {host(func(_, _, d string) {
			writeTree(t, d, map[string]string{"typescript/package.json": pkgJSON("typescript", "6.0.0")})
		}), "", "available", ""},
		"service mismatch": {host(func(_, _, d string) {
			writeTree(t, d, map[string]string{"@vtsls/language-service/package.json": pkgJSON(vtslsServiceName, "0.2.9")})
		}), "", "unsupported", "different language service (0.2.9)"},
		"hostile version text": {host(func(_, s, _ string) {
			writeTree(t, s, map[string]string{"package.json": pkgJSON(vtslsName, "0.3.0\n<b>x</b>")})
		}), "", "unsupported", "Found vtsls (unknown)"},
		"foreign package": {host(func(_, s, _ string) {
			writeTree(t, s, map[string]string{"package.json": pkgJSON("evil", "0.3.0")})
		}), "", "unsupported", "could not be verified"},
		"incomplete install": {host(func(_, _, d string) { _ = os.Remove(filepath.Join(d, "typescript/lib/_tsserver.js")) }), "", "unsupported", "incomplete"},
	} {
		e := env
		e.NodeDir = tc.node
		p, err := DiscoverTypeScript(root, tc.path, e)
		if err != nil || p.Availability() != tc.state || !strings.Contains(p.Provider().Notice, tc.notice) {
			t.Errorf("%s: %v %s %q", name, err, p.Availability(), p.Provider().Notice)
			continue
		}
		if tc.state == "available" {
			continue
		}
		if _, ok := p.Server(); ok || !p.Provider().Coverage.Partial {
			t.Errorf("%s: unavailable plan offers a server or full coverage", name)
		}
	}
	// Sources alone are enough; without any TypeScript or JavaScript file
	// vtsls is a muted unsupported, never a request to install.
	if p, _ = DiscoverTypeScript(goplsRoot(t, map[string]string{"a.ts": ""}), path, env); p.Availability() != "available" {
		t.Fatal("no package", p.Availability())
	}
	for _, path := range [][]string{path, {t.TempDir()}} {
		if p, _ = DiscoverTypeScript(goplsRoot(t, map[string]string{"a.py": ""}), path, env); p.Availability() != "unsupported" {
			t.Fatal("no sources", p.Availability())
		}
	}
	for _, bad := range []TSEnvironment{{Home: env.Home, NodeDir: "relative"}, {Home: "", NodeDir: ""}} {
		if _, err := DiscoverTypeScript(root, path, bad); errCode(err) != "invalidArgument" {
			t.Fatal(err)
		}
	}
}

// A flattened project-local install (npm i -D @vtsls/language-server) is
// executable project content; node comes from the reviewed path, skipping a
// version-manager shim, and an explicit NodeDir wins over both.
func TestTSProjectLocalFlattenedLayout(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"package.json": "{}", "src/a.mts": ""})
	base := root.Project().Root
	nm := filepath.Join(base, "node_modules")
	writeVtsls(t, filepath.Join(nm, "@vtsls/language-server"), nm, vtslsName, "0.3.0", "0.3.0", "5.9.3")
	if err := os.MkdirAll(filepath.Join(nm, ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../@vtsls/language-server/bin/vtsls.js", filepath.Join(nm, ".bin/vtsls")); err != nil {
		t.Fatal(err)
	}
	shim, nodeA, nodeB := t.TempDir(), t.TempDir(), t.TempDir()
	writeTree(t, shim, map[string]string{"node": "#!/bin/sh\n"})
	_ = os.Chmod(filepath.Join(shim, "node"), 0o755)
	writeNode(t, nodeA, "20.11.1")
	writeNode(t, nodeB, "24.1.0")
	nodeA, _ = filepath.EvalSymlinks(nodeA)
	nodeB, _ = filepath.EvalSymlinks(nodeB)
	path := []string{shim, filepath.Join(nodeA, "bin")}
	p, err := DiscoverTypeScript(root, path, TSEnvironment{Home: base})
	if err != nil || p.Availability() != "available" || !p.ProjectLocal() || p.NodeVersion() != "20.11.1" {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	cfg, _ := p.Server()
	if cfg.Executable != filepath.Join(nodeA, "bin/node") || cfg.Args[0] != filepath.Join(nm, "@vtsls/language-server/bin/vtsls.js") ||
		cfg.Toolchain[5].Executable != filepath.Join(nm, "typescript/lib/typescript.js") || !slices.Equal(cfg.ConfigPaths, []string{"package.json"}) {
		t.Fatalf("%+v", cfg)
	}
	if p, _ = DiscoverTypeScript(root, path, TSEnvironment{Home: base, NodeDir: filepath.Join(nodeB, "bin")}); p.NodeVersion() != "24.1.0" {
		t.Fatal(p.NodeVersion())
	}
	// A project-local link that escapes the root is not project content.
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	writeVtsls(t, filepath.Join(outside, "node_modules/@vtsls/language-server"), filepath.Join(outside, "node_modules"), vtslsName, "0.3.0", "0.3.0", "5.9.3")
	root2 := goplsRoot(t, map[string]string{"package.json": "{}"})
	_ = os.MkdirAll(filepath.Join(root2.Project().Root, "node_modules/.bin"), 0o755)
	_ = os.Symlink(filepath.Join(outside, "node_modules/@vtsls/language-server/bin/vtsls.js"), filepath.Join(root2.Project().Root, "node_modules/.bin/vtsls"))
	if entry, local, _ := resolveVtsls(root2, nil); entry != "" || local {
		t.Fatalf("escaped link resolved: %s", entry)
	}
}

// The fixed adapter settings are bound into approval, launched exactly as
// discovered (no shell), advertised in initialize and the only
// workspace/configuration answer; gopls (no settings) keeps its policy.
func TestTSSessionTrustLaunchAndSettings(t *testing.T) {
	skipUnlessProduction(t)
	e := newEnv(t, SessionOptions{})
	var params map[string]any
	replies := make(chan map[string]any, 4)
	ask := make(chan [3]string)
	e.script = func(f *fakeSrv) {
		m, _ := f.read()
		params, _ = m["params"].(map[string]any)
		id, _ := json.Marshal(m["id"])
		f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + defaultInit + `}`)
		f.read() // initialized
		for q := range ask {
			replies <- f.request(q[0], q[1], q[2])
		}
		serveInit(defaultInit)(f)
	}
	root := goplsRoot(t, map[string]string{"package.json": "{}", "a.ts": ""})
	path, env := tsHost(t)
	p, _ := DiscoverTypeScript(root, path, env)
	cfg, ok := p.Server()
	if !ok {
		t.Fatal(p.Provider().Notice)
	}
	if _, err := e.mgr.Attach(context.Background(), root, cfg); errCode(err) != "approvalRequired" {
		t.Fatal("unapproved server attached", err)
	}
	if e.launches() != 0 {
		t.Fatal("launched before approval")
	}
	// Approving without the settings (an older grant) is not this server.
	stale := cfg
	stale.Settings = nil
	fp, _ := Fingerprint(root, stale.Tools())
	approve(t, e.trust, root, stale.Tools(), fp, "session")
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err == nil {
		t.Fatal("grant without fixed settings accepted")
	}
	fp, _ = Fingerprint(root, cfg.Tools())
	approve(t, e.trust, root, cfg.Tools(), fp, "session")
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	defer close(ask)
	f := e.next()
	if f.inv.Executable != cfg.Executable || !slices.Equal(f.inv.Args, cfg.Args) || !slices.Equal(f.inv.Env, cfg.command().environment()) {
		t.Fatalf("%+v", f.inv)
	}
	for _, v := range append(append([]string{f.inv.Executable}, f.inv.Args...), f.inv.Env...) {
		if strings.Contains(v, "npx") || strings.HasSuffix(v, "/sh") || strings.Contains(v, "shims") {
			t.Fatalf("shell or shim in launch: %q", v)
		}
	}
	caps, _ := params["capabilities"].(map[string]any)
	ws, _ := caps["workspace"].(map[string]any)
	td, _ := caps["textDocument"].(map[string]any)
	if ws["configuration"] != true || td["publishDiagnostics"] == nil || len(caps) != 3 {
		t.Fatalf("configuration or diagnostics not advertised: %v", caps)
	}
	ask <- [3]string{"1", "workspace/configuration", `{"items":[{"section":"typescript"},{"section":"vtsls.autoUseWorkspaceTsdk"},{"section":"javascript"},{"scopeUri":"` + uriOf(root, "a.ts") + `"},{"section":"typescript.tsserver.pluginPaths"},{"section":""}]}`}
	r := <-replies
	res, _ := r["result"].([]any)
	if len(res) != 6 {
		t.Fatalf("%v", r)
	}
	ts, _ := res[0].(map[string]any)
	whole, _ := res[3].(map[string]any)
	if ts["disableAutomaticTypeAcquisition"] != true || res[1] != false || res[2] != nil || whole["vtsls"] == nil || len(res[4].([]any)) != 0 || res[5].(map[string]any)["typescript"] == nil {
		t.Fatalf("%v", res)
	}
	g := ServerConfig{ProviderID: goplsProviderID, Executable: "/x/gopls", AdapterPolicy: "gopls-v1"}
	if c := g.command(); c.AdapterPolicy != "gopls-v1" || !strings.Contains(cfg.command().AdapterPolicy, "|client:publish-diagnostics|settings:{") {
		t.Fatal("settings-free policy changed", c.AdapterPolicy)
	}
}

func TestTSDocumentResolution(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"web/package.json": "{}", "web/src/a.ts": "", "other/b.ts": "", "c.ts": ""})
	path, env := tsHost(t)
	p, _ := DiscoverTypeScript(root, path, env)
	d := p.Documents(&SessionManager{})
	for file, want := range map[string]bool{
		"web/src/a.ts": true, "web/x.jsx": true,
		"other/b.ts": true, "c.ts": true, // the whole project, not only the package
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
	if ev.Versions["@vtsls/language-server"] == "" || ev.Versions["typescript"] == "" {
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

	// Unversioned vtsls positions stay uncertain and are bound to the mirror at receipt.
	b.push("a.ts", "", errAt)
	b.await(t, "unversioned", func(r []ResultSet) bool {
		return len(r[0].Diagnostics) == 1 && r[0].Diagnostics[0].Freshness == "uncertain" && r[0].Diagnostics[0].Range != nil && r[0].Diagnostics[0].Basis.Version == nil && r[0].Diagnostics[0].Basis.ObservedVersion != nil && *r[0].Diagnostics[0].Basis.ObservedVersion == 2
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

// Synthetic: what the observed vtsls sends for an opened .mjs/.ts draft (no
// version). Results are uncertain and unpositioned; the empty push clears.
func TestTSBridgeUnversionedJavaScriptClears(t *testing.T) {
	b := newTSBridgeEnv(t, map[string]string{"a.mjs": "x", "b.ts": "x"})
	ctx, c := context.Background(), b.h.snap.Context
	for i, name := range []string{"a.mjs", "b.ts"} {
		if _, err := b.h.OpenDocument(ctx, c, name, "d"+name, 1, "let x = ;\n", ""); err != nil {
			t.Fatal(err)
		}
		b.push(name, "", `{"range":{"start":{"line":0,"character":8},"end":{"line":0,"character":9}},"severity":1,"code":1109,"source":"ts","message":"Expression expected."}`)
		b.await(t, name+" uncertain", func(r []ResultSet) bool {
			return len(r) == i+1 && len(r[i].Diagnostics) == 1 && r[i].Scope.Path == name
		})
		d := b.sets(t)[i].Diagnostics[0]
		if d.Freshness != "uncertain" || d.Range == nil || d.Basis.Version != nil || d.Basis.ObservedVersion == nil || *d.Basis.ObservedVersion != 1 || d.Code != "1109" || d.Source != "typescript/ts" {
			t.Fatalf("%+v", d)
		}
		if _, err := b.h.ChangeDocument(ctx, c, "d"+name, 2, "let x = 1;\n"); err != nil {
			t.Fatal(err)
		}
		b.push(name, "", ``)
		b.await(t, name+" cleared", func(r []ResultSet) bool { return len(r) == i+1 && len(r[i].Diagnostics) == 0 })
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

// HostNode follows tsNode: the first real node decides, and only a new
// enough one is offered to managed installs.
func TestHostNode(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	old, cur, empty := filepath.Join(base, "old"), filepath.Join(base, "cur"), filepath.Join(base, "empty")
	writeNode(t, old, "16.20.2")
	writeNode(t, cur, "22.23.2")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if prefix, v := HostNode([]string{empty, filepath.Join(cur, "bin"), filepath.Join(old, "bin")}); prefix != cur || v != "22.23.2" {
		t.Fatal(prefix, v)
	}
	if prefix, _ := HostNode([]string{filepath.Join(old, "bin"), filepath.Join(cur, "bin")}); prefix != "" {
		t.Fatal("an older node that vtsls would run on was skipped", prefix)
	}
	if prefix, _ := HostNode([]string{empty}); prefix != "" {
		t.Fatal(prefix)
	}
}
