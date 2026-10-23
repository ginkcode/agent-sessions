package codediagnostics

// SYNTHETIC fixtures: the fake node, Python and pyright package trees below
// prove this adapter's handling only. They do not claim real-server behavior.
// TestPyrightRealSmoke is the only test that runs a real Pyright, and only when
// CD_PYRIGHT_SMOKE_PATH names reviewed directories holding pyright-langserver
// and node; nothing is ever installed.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writePyright lays out a SYNTHETIC pyright npm package at dir. The .js bodies
// are never loaded.
func writePyright(t *testing.T, dir, name, version string) {
	t.Helper()
	writeTree(t, dir, map[string]string{
		"package.json":                               pkgJSON(name, version),
		"langserver.index.js":                        "#!/usr/bin/env node\nglobal.__rootDirectory = __dirname + '/dist/';\nrequire('./dist/pyright-langserver');\n",
		"index.js":                                   "#!/usr/bin/env node\n",
		"dist/pyright-langserver.js":                 "synthetic",
		"dist/pyright-internal.js":                   "synthetic",
		"dist/typeshed-fallback/stdlib/builtins.pyi": "",
	})
}

// pyrightHost builds a SYNTHETIC global npm install: <prefix>/bin/pyright-langserver
// links to <prefix>/lib/node_modules/pyright/langserver.index.js beside
// <prefix>/bin/node. Nothing in it is ever executed.
func pyrightHost(t *testing.T) ([]string, PyrightEnvironment, string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	prefix := filepath.Join(base, "prefix")
	pkg := filepath.Join(prefix, "lib", "node_modules", "pyright")
	writePyright(t, pkg, pyrightName, "1.1.414")
	writeNode(t, prefix, "22.23.2")
	if err := os.Symlink("../lib/node_modules/pyright/langserver.index.js", filepath.Join(prefix, "bin", "pyright-langserver")); err != nil {
		t.Fatal(err)
	}
	return []string{filepath.Join(prefix, "bin")}, PyrightEnvironment{Home: base}, pkg
}

// writeVenv creates a SYNTHETIC virtual environment below root whose
// bin/python links to a fake native interpreter outside it.
func writeVenv(t *testing.T, root AuthorizedRoot, name string) (python, real string) {
	t.Helper()
	host, _ := filepath.EvalSymlinks(t.TempDir())
	real = filepath.Join(host, "bin", "python3.12")
	writeTree(t, host, map[string]string{"bin/python3.12": "\xcf\xfa\xed\xfe synthetic python"})
	if err := os.Chmod(real, 0o755); err != nil {
		t.Fatal(err)
	}
	venv := filepath.Join(root.Project().Root, name)
	writeTree(t, venv, map[string]string{"pyvenv.cfg": "home = " + filepath.Dir(real) + "\n", "lib/python3.12/site-packages/x.py": ""})
	if err := os.MkdirAll(filepath.Join(venv, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	python = filepath.Join(venv, "bin", "python")
	if err := os.Symlink(real, python); err != nil {
		t.Fatal(err)
	}
	return python, real
}

func toolchainOf(cfg ServerConfig) (ids, files []string) {
	for _, tool := range cfg.Toolchain {
		ids, files = append(ids, tool.ProviderID), append(files, tool.Executable)
	}
	return ids, files
}

func settingsOf(t *testing.T, cfg ServerConfig) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(cfg.Settings, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPyrightProjectDiscovery(t *testing.T) {
	cases := map[string]struct {
		files            map[string]string
		configs          []string
		partial, sources bool
	}{
		"pyproject":            {map[string]string{"pyproject.toml": "", "a.py": ""}, []string{"pyproject.toml"}, false, true},
		"pyrightconfig":        {map[string]string{"pyrightconfig.json": "{}", "pkg/a.pyi": ""}, []string{"pyrightconfig.json"}, false, true},
		"ruff config ignored":  {map[string]string{"ruff.toml": "", "a.py": ""}, nil, false, true},
		"environments skipped": {map[string]string{"pyproject.toml": "", ".venv/lib/x/pyrightconfig.json": "", "venv/pyproject.toml": "", "node_modules/x/pyproject.toml": ""}, []string{"pyproject.toml"}, false, false},
		"no python":            {map[string]string{"a.ts": "", "package.json": "{}"}, nil, false, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, err := discoverProjectDirs(goplsRoot(t, c.files), pyrightProjectMarkers)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.configs, c.configs) || w.partial != c.partial || w.sources != c.sources {
				t.Fatalf("%+v", w)
			}
		})
	}
}

func TestDiscoverPyrightAvailability(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"pyproject.toml": "[tool.pyright]\n", "pyrightconfig.json": "{}", "svc/pyproject.toml": "", "a.py": ""})
	path, env, pkg := pyrightHost(t)
	prefix := filepath.Dir(path[0])
	p, err := DiscoverPyright(root, path, env)
	if err != nil || p.Availability() != "available" || p.ProjectLocal() || p.Version() != "1.1.414" || p.NodeVersion() != "22.23.2" || p.Environment() != "" {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	cfg, ok := p.Server()
	// node runs the package's own entry: no shell, npx or bin shim in argv.
	if !ok || cfg.ProviderID != pyrightProviderID || cfg.Executable != filepath.Join(prefix, "bin", "node") ||
		!slices.Equal(cfg.Args, []string{filepath.Join(pkg, "langserver.index.js"), "--stdio"}) || !cfg.DeclareDiagnostics {
		t.Fatalf("%+v", cfg)
	}
	// No system directories: Pyright's python3/python fallback finds nothing unbound.
	if want := []string{"HOME=" + env.Home, "PATH=" + filepath.Join(prefix, "bin"), "NODE_DISABLE_COMPILE_CACHE=1"}; !slices.Equal(cfg.Env, want) {
		t.Fatalf("env %v", cfg.Env)
	}
	// Only root-level Pyright configurations are read, so only they are bound.
	if !slices.Equal(cfg.ConfigPaths, []string{"pyproject.toml", "pyrightconfig.json"}) || cfg.AdapterPolicy != pyrightPolicy+"no-interpreter" {
		t.Fatalf("%v %s", cfg.ConfigPaths, cfg.Policy())
	}
	ids, files := toolchainOf(cfg)
	if !slices.Equal(ids, []string{"pyright-toolchain-pyright-entry", "pyright-toolchain-pyright-langserver", "pyright-toolchain-pyright-internal"}) ||
		!slices.Equal(files, []string{filepath.Join(pkg, "langserver.index.js"), filepath.Join(pkg, "dist/pyright-langserver.js"), filepath.Join(pkg, "dist/pyright-internal.js")}) {
		t.Fatalf("toolchain %v %v", ids, files)
	}
	s := settingsOf(t, cfg)
	py, _ := s["python"].(map[string]any)
	an, _ := py["analysis"].(map[string]any)
	pr, _ := s["pyright"].(map[string]any)
	if py["pythonPath"] != nil || an["diagnosticMode"] != "openFilesOnly" || an["typeCheckingMode"] != "basic" || pr["disableLanguageServices"] != true ||
		an["diagnosticSeverityOverrides"].(map[string]any)["reportMissingImports"] != "none" {
		t.Fatalf("settings %s", cfg.Settings)
	}
	if err := validateCommand(root, cfg.Tools(), cfg.command()); err != nil {
		t.Fatal(err)
	}
	st := p.Provider()
	if st.ProviderID != pyrightProviderID || st.Coverage.Kind != "openedDocuments" || st.Coverage.Partial || !slices.Equal(st.Coverage.Roots, []string{""}) ||
		!slices.Equal(st.DocumentExtensions, []string{".py", ".pyi"}) || !strings.Contains(st.Notice, "basic mode") ||
		!strings.Contains(st.Notice, "No project virtual environment") || !strings.Contains(st.Notice, "opened documents only") {
		t.Fatalf("%+v", st)
	}
	if !p.covers("svc/deep/a.py") || !p.covers("a.pyi") {
		t.Fatal("whole project not covered")
	}
	// A loaded bundle or bound configuration changing changes the fingerprint.
	before, _ := Fingerprint(root, cfg.Tools())
	writeTree(t, pkg, map[string]string{"dist/pyright-internal.js": "changed"})
	mid, err := Fingerprint(root, cfg.Tools())
	if err != nil || mid == before {
		t.Fatal("bundle change kept the fingerprint", err)
	}
	writeTree(t, root.Project().Root, map[string]string{"pyrightconfig.json": `{"typeCheckingMode":"strict"}`})
	if after, err := Fingerprint(root, cfg.Tools()); err != nil || after == mid {
		t.Fatal("config change kept the fingerprint", err)
	}
	// A shipped vendor chunk is bound too.
	writeTree(t, pkg, map[string]string{"dist/vendor.js": "synthetic"})
	if p, _ = DiscoverPyright(root, path, env); len(p.config.Toolchain) != 4 || p.config.Toolchain[3].Executable != filepath.Join(pkg, "dist/vendor.js") {
		t.Fatalf("%+v", p.config.Toolchain)
	}

	// Independent unavailability: never a server, never a process.
	host := func(mutate func(prefix, pkg string)) []string {
		path, _, pkg := pyrightHost(t)
		mutate(filepath.Dir(path[0]), pkg)
		return path
	}
	shim := t.TempDir()
	writeTree(t, shim, map[string]string{"mise": "\xcf\xfa\xed\xfe launcher"})
	_ = os.Chmod(filepath.Join(shim, "mise"), 0o755)
	_ = os.Symlink("mise", filepath.Join(shim, "pyright-langserver"))
	oldNode := t.TempDir()
	writeNode(t, oldNode, "12.22.12")
	for name, tc := range map[string]struct {
		path          []string
		node          string
		state, notice string
	}{
		"missing":             {[]string{t.TempDir()}, "", "missingTool", "npm install -g pyright"},
		"relative path":       {[]string{"bin", "../bin"}, "", "missingTool", "Pyright is not installed"},
		"shim":                {[]string{shim}, "", "unsupported", "version-manager shim"},
		"shim before install": {append([]string{shim}, path...), "", "available", ""},
		"missing node":        {host(func(p, _ string) { _ = os.Remove(filepath.Join(p, "bin", "node")) }), "", "missingTool", "Node.js, required by Pyright"},
		"old node":            {path, filepath.Join(oldNode, "bin"), "unsupported", "Node.js 12.22.12 is too old"},
		"foreign package": {host(func(_, k string) {
			writeTree(t, k, map[string]string{"package.json": pkgJSON("basedpyright", "1.1.414")})
		}), "", "unsupported", "could not be verified"},
		"hostile version": {host(func(_, k string) {
			writeTree(t, k, map[string]string{"package.json": pkgJSON(pyrightName, "1.1\n<b>x</b>")})
		}), "", "unsupported", "Found Pyright (unknown)"},
		"other version":     {host(func(_, k string) { writeTree(t, k, map[string]string{"package.json": pkgJSON(pyrightName, "1.1.300")}) }), "", "available", ""},
		"incomplete bundle": {host(func(_, k string) { _ = os.Remove(filepath.Join(k, "dist/pyright-internal.js")) }), "", "unsupported", "incomplete"},
		"no typeshed":       {host(func(_, k string) { _ = os.RemoveAll(filepath.Join(k, "dist/typeshed-fallback")) }), "", "unsupported", "incomplete"},
	} {
		e := env
		e.NodeDir = tc.node
		p, err := DiscoverPyright(root, tc.path, e)
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
	// Without any Python Pyright is a muted unsupported, never a request to install.
	for _, path := range [][]string{path, {t.TempDir()}} {
		if p, _ = DiscoverPyright(goplsRoot(t, map[string]string{"a.ts": ""}), path, env); p.Availability() != "unsupported" || !strings.Contains(p.Provider().Notice, "No Python file") {
			t.Fatal("no Python", p.Availability())
		}
	}
	for _, bad := range []PyrightEnvironment{{}, {Home: "relative"}, {Home: env.Home, NodeDir: "/x/../y"}} {
		if _, err := DiscoverPyright(root, path, bad); errCode(err) != "invalidArgument" {
			t.Fatal(err)
		}
	}
}

// Without a project environment the first native python3 (then python) on the
// reviewed path becomes pythonPath, resolved and bound; shims are skipped.
// With none, anything executable Pyright could still find on its fixed PATH
// (the node directory) is bound and missing imports stay unreported.
func TestPyrightHostPython(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"a.py": ""})
	path, env, _ := pyrightHost(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	shim, native := filepath.Join(base, "shims"), filepath.Join(base, "py")
	writeTree(t, shim, map[string]string{"python3": "#!/bin/sh\nexec pyenv exec python3 \"$@\"\n"})
	writeTree(t, native, map[string]string{"python3.13": "\xcf\xfa\xed\xfe synthetic python", "python": "\xcf\xfa\xed\xfe synthetic python2"})
	for _, f := range []string{filepath.Join(shim, "python3"), filepath.Join(native, "python3.13"), filepath.Join(native, "python")} {
		_ = os.Chmod(f, 0o755)
	}
	_ = os.Symlink("python3.13", filepath.Join(native, "python3"))
	p, _ := DiscoverPyright(root, append([]string{shim, native}, path...), env)
	cfg, ok := p.Server()
	ids, files := toolchainOf(cfg)
	real := filepath.Join(native, "python3.13")
	if !ok || p.Interpreter() != real || ids[len(ids)-1] != "pyright-toolchain-host-python" || files[len(files)-1] != real || !strings.HasSuffix(cfg.AdapterPolicy, ":host-python") {
		t.Fatalf("%v %v %s %s", ids, files, cfg.AdapterPolicy, p.Interpreter())
	}
	s := settingsOf(t, cfg)
	py, _ := s["python"].(map[string]any)
	if py["pythonPath"] != real || py["analysis"].(map[string]any)["diagnosticSeverityOverrides"] != nil || !strings.Contains(p.Provider().Notice, "Pyright runs "+real) {
		t.Fatalf("%s %q", cfg.Settings, p.Provider().Notice)
	}
	if err := validateCommand(root, cfg.Tools(), cfg.command()); err != nil {
		t.Fatal(err)
	}
	before, _ := Fingerprint(root, cfg.Tools())
	writeTree(t, native, map[string]string{"python3.13": "\xcf\xfa\xed\xfe other python"})
	if after, err := Fingerprint(root, cfg.Tools()); err != nil || after == before {
		t.Fatal("interpreter change kept the fingerprint", err)
	}
	// python3 anywhere wins over python earlier on the path.
	_ = os.Remove(filepath.Join(native, "python3"))
	other := filepath.Join(base, "other")
	writeTree(t, other, map[string]string{"python3": "\xcf\xfa\xed\xfe synthetic"})
	_ = os.Chmod(filepath.Join(other, "python3"), 0o755)
	if p, _ = DiscoverPyright(root, append([]string{native, other}, path...), env); p.Interpreter() != filepath.Join(other, "python3") {
		t.Fatal(p.Interpreter())
	}
	// Only a shim in the node directory: no interpreter, but the shim is bound.
	writeTree(t, path[0], map[string]string{"python3": "#!/bin/sh\n"})
	_ = os.Chmod(filepath.Join(path[0], "python3"), 0o755)
	p, _ = DiscoverPyright(root, append([]string{shim}, path...), env)
	cfg, _ = p.Server()
	ids, _ = toolchainOf(cfg)
	if p.Interpreter() != "" || ids[len(ids)-1] != "pyright-toolchain-path-python3" || !strings.HasSuffix(cfg.AdapterPolicy, ":path-python") ||
		!strings.Contains(string(cfg.Settings), "reportMissingImports") || !strings.Contains(p.Provider().Notice, "bundled stubs") {
		t.Fatalf("%v %s %q", ids, cfg.AdapterPolicy, p.Provider().Notice)
	}
}

// A contained virtual environment becomes pythonPath; its interpreter and
// pyvenv.cfg are bound. Links, scripts and missing interpreters are not used,
// and nothing is ever run to find out.
func TestPyrightVirtualEnvironment(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"pyproject.toml": "", "src/a.py": ""})
	path, env, _ := pyrightHost(t)
	python, real := writeVenv(t, root, ".venv")
	p, err := DiscoverPyright(root, path, env)
	if err != nil || p.Availability() != "available" || p.Environment() != ".venv" {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	cfg, _ := p.Server()
	ids, files := toolchainOf(cfg)
	if !slices.Equal(cfg.ConfigPaths, []string{".venv/pyvenv.cfg", "pyproject.toml"}) || ids[len(ids)-1] != "pyright-toolchain-venv-python" ||
		files[len(files)-1] != python || !strings.HasSuffix(cfg.AdapterPolicy, ":venv-python") {
		t.Fatalf("%+v", cfg)
	}
	s := settingsOf(t, cfg)
	py, _ := s["python"].(map[string]any)
	if py["pythonPath"] != python || py["analysis"].(map[string]any)["diagnosticSeverityOverrides"] != nil {
		t.Fatalf("settings %s", cfg.Settings)
	}
	if n := p.Provider().Notice; !strings.Contains(n, "runs the project environment's Python (.venv)") || strings.Contains(n, "No project virtual environment") {
		t.Fatal(n)
	}
	if err := validateCommand(root, cfg.Tools(), cfg.command()); err != nil {
		t.Fatal(err)
	}
	before, _ := Fingerprint(root, cfg.Tools())
	writeTree(t, filepath.Dir(filepath.Dir(python)), map[string]string{"pyvenv.cfg": "home = /elsewhere\n"})
	mid, err := Fingerprint(root, cfg.Tools())
	if err != nil || mid == before {
		t.Fatal("pyvenv.cfg change kept the fingerprint", err)
	}
	writeTree(t, filepath.Dir(filepath.Dir(real)), map[string]string{"bin/python3.12": "\xcf\xfa\xed\xfe upgraded"})
	if after, err := Fingerprint(root, cfg.Tools()); err != nil || after == mid {
		t.Fatal("interpreter change kept the fingerprint", err)
	}
	// venv is the second choice.
	root2 := goplsRoot(t, map[string]string{"a.py": ""})
	writeVenv(t, root2, "venv")
	if p, _ = DiscoverPyright(root2, path, env); p.Environment() != "venv" {
		t.Fatal(p.Provider().Notice)
	}

	for name, mutate := range map[string]func(base string){
		"linked environment": func(base string) {
			outside := goplsRoot(t, nil)
			writeVenv(t, outside, ".venv")
			_ = os.Symlink(filepath.Join(outside.Project().Root, ".venv"), filepath.Join(base, ".venv"))
		},
		"script python": func(base string) {
			writeTree(t, base, map[string]string{".venv/pyvenv.cfg": "", ".venv/bin/python": "#!/bin/sh\nexec pyenv exec python \"$@\"\n"})
			_ = os.Chmod(filepath.Join(base, ".venv/bin/python"), 0o755)
		},
		"no python": func(base string) {
			writeTree(t, base, map[string]string{".venv/pyvenv.cfg": "", ".venv/bin/pip": ""})
		},
	} {
		r := goplsRoot(t, map[string]string{"a.py": ""})
		mutate(r.Project().Root)
		p, err := DiscoverPyright(r, path, env)
		cfg, ok := p.Server()
		if err != nil || !ok || p.Environment() != "" || strings.Contains(string(cfg.Settings), "pythonPath") || slices.Contains(cfg.ConfigPaths, ".venv/pyvenv.cfg") ||
			!strings.Contains(p.Provider().Notice, "so it is not used") {
			t.Errorf("%s: %v %+v %q", name, err, cfg, p.Provider().Notice)
		}
	}
	// A directory without pyvenv.cfg is not an environment and is silently ignored.
	r := goplsRoot(t, map[string]string{"a.py": "", ".venv/bin/x": ""})
	if p, _ = DiscoverPyright(r, path, env); p.Environment() != "" || strings.Contains(p.Provider().Notice, "not used") {
		t.Fatal(p.Provider().Notice)
	}
}

// A project-local install (npm i -D pyright) is executable project content; a
// link escaping the root is not.
func TestPyrightProjectLocalPackage(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"a.py": "", "package.json": "{}"})
	nm := filepath.Join(root.Project().Root, "node_modules")
	writePyright(t, filepath.Join(nm, "pyright"), pyrightName, "1.1.400")
	_ = os.MkdirAll(filepath.Join(nm, ".bin"), 0o755)
	if err := os.Symlink("../pyright/langserver.index.js", filepath.Join(nm, ".bin/pyright-langserver")); err != nil {
		t.Fatal(err)
	}
	path, env, _ := pyrightHost(t)
	p, err := DiscoverPyright(root, path, env)
	if err != nil || p.Availability() != "available" || !p.ProjectLocal() || p.Version() != "1.1.400" {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	if cfg, _ := p.Server(); cfg.Args[0] != filepath.Join(nm, "pyright/langserver.index.js") {
		t.Fatalf("%+v", cfg)
	}
	outside := goplsRoot(t, nil).Project().Root
	writePyright(t, filepath.Join(outside, "node_modules/pyright"), pyrightName, "1.1.414")
	root2 := goplsRoot(t, map[string]string{"a.py": ""})
	_ = os.MkdirAll(filepath.Join(root2.Project().Root, "node_modules/.bin"), 0o755)
	_ = os.Symlink(filepath.Join(outside, "node_modules/pyright/langserver.index.js"), filepath.Join(root2.Project().Root, "node_modules/.bin/pyright-langserver"))
	if entry, local, _ := resolvePyright(root2, nil); entry != "" || local {
		t.Fatalf("escaped link resolved: %s", entry)
	}
}

// The app-managed layout (<install>/node_modules/.bin on the reviewed path,
// last) resolves to the package entry and uses a reviewed-path node.
func TestPyrightManagedLayout(t *testing.T) {
	skipUnlessProduction(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	install := filepath.Join(base, "tools", "pyright-1.1.414")
	writePyright(t, filepath.Join(install, "node_modules/pyright"), pyrightName, "1.1.414")
	_ = os.MkdirAll(filepath.Join(install, "node_modules/.bin"), 0o755)
	if err := os.Symlink("../pyright/langserver.index.js", filepath.Join(install, "node_modules/.bin/pyright-langserver")); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(base, "node")
	writeNode(t, node, "20.11.1")
	p, err := DiscoverPyright(goplsRoot(t, map[string]string{"a.py": ""}), []string{filepath.Join(node, "bin"), filepath.Join(install, "node_modules/.bin")}, PyrightEnvironment{Home: base})
	if err != nil || p.Availability() != "available" || p.NodeVersion() != "20.11.1" {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	if cfg, _ := p.Server(); cfg.Executable != filepath.Join(node, "bin/node") || cfg.Args[0] != filepath.Join(install, "node_modules/pyright/langserver.index.js") {
		t.Fatalf("%+v", cfg)
	}
}

// The fixed settings are bound into approval, advertised in initialize and are
// the only answer to the sections Pyright asks for.
func TestPyrightSessionTrustLaunchAndSettings(t *testing.T) {
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
	root := goplsRoot(t, map[string]string{"pyproject.toml": "", "a.py": ""})
	python, _ := writeVenv(t, root, ".venv")
	path, env, _ := pyrightHost(t)
	p, _ := DiscoverPyright(root, path, env)
	cfg, ok := p.Server()
	if !ok {
		t.Fatal(p.Provider().Notice)
	}
	if _, err := e.mgr.Attach(context.Background(), root, cfg); errCode(err) != "approvalRequired" || e.launches() != 0 {
		t.Fatal("unapproved server attached", err)
	}
	// A grant without the bound interpreter is not this server.
	partial := cfg
	partial.Toolchain = partial.Toolchain[:len(partial.Toolchain)-1]
	fp, _ := Fingerprint(root, partial.Tools())
	approve(t, e.trust, root, partial.Tools(), fp, "session")
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err == nil {
		t.Fatal("grant without the interpreter accepted")
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
	caps, _ := params["capabilities"].(map[string]any)
	ws, _ := caps["workspace"].(map[string]any)
	if ws["configuration"] != true || params["rootUri"] != uriOf(root, "") {
		t.Fatalf("%v", params)
	}
	scope := `"scopeUri":"` + uriOf(root, "") + `"`
	ask <- [3]string{"1", "workspace/configuration", `{"items":[{` + scope + `,"section":"python"},{` + scope + `,"section":"python.analysis"},{` + scope + `,"section":"pyright"},{"section":"basedpyright"}]}`}
	r := <-replies
	res, _ := r["result"].([]any)
	if len(res) != 4 {
		t.Fatalf("%v", r)
	}
	py, _ := res[0].(map[string]any)
	an, _ := res[1].(map[string]any)
	pr, _ := res[2].(map[string]any)
	if py["pythonPath"] != python || an["diagnosticMode"] != "openFilesOnly" || an["typeCheckingMode"] != "basic" || pr["disableLanguageServices"] != true || res[3] != nil {
		t.Fatalf("%v", res)
	}
	// A scope outside the root is rejected.
	ask <- [3]string{"2", "workspace/configuration", `{"items":[{"scopeUri":"file:///etc","section":"python"}]}`}
	if r := <-replies; r["error"] == nil {
		t.Fatalf("%v", r)
	}
}

type pyrightBridgeEnv struct{ *bridgeEnv }

func newPyrightBridgeEnv(t *testing.T, files map[string]string) *pyrightBridgeEnv {
	t.Helper()
	b := &pyrightBridgeEnv{&bridgeEnv{notices: make(chan string, 64)}}
	var bridge *PyrightBridge
	e := newEnv(t, SessionOptions{Notifications: func(n SessionNotification) { bridge.Handle(n) }})
	b.sessionEnv = e
	b.root, b.cfg = e.project()
	b.cfg.ProviderID = pyrightProviderID
	e.approve(b.root, b.cfg)
	writeTree(t, b.root.Project().Root, files)
	b.h = NewManagerForTest(t, b.root)
	bridge = NewPyrightBridge(b.root, b.h.Manager)
	bridge.Notice = func(s string) { b.notices <- s }
	b.h.SetDocumentProvider(SessionDocuments{Sessions: e.mgr, Resolve: func(r AuthorizedRoot, path string) (DocumentServer, bool) {
		lang, ok := PythonLanguageID(path)
		return DocumentServer{Config: b.cfg, LanguageID: lang}, ok
	}})
	if _, err := e.mgr.Attach(context.Background(), b.root, b.cfg); err != nil {
		t.Fatal(err)
	}
	b.srv = e.next()
	return b
}

// Synthetic fake server: versioned pushes are current and positioned, stale
// ones rejected, unversioned ones uncertain; empty pushes clear; configs, notebooks, unopened
// files, other providers and out-of-root URIs never create state.
func TestPyrightBridgeSynthetic(t *testing.T) {
	b := newPyrightBridgeEnv(t, map[string]string{"a.py": "x", "pyproject.toml": "", "n.ipynb": ""})
	ctx, c := context.Background(), b.h.snap.Context
	if _, err := b.h.OpenDocument(ctx, c, "a.py", "d1", 1, "x: int = 1\ny: int = \"s\"\n", ""); err != nil {
		t.Fatal(err)
	}
	diag := `{"range":{"start":{"line":1,"character":9},"end":{"line":1,"character":12}},"severity":1,"code":"reportAssignmentType","codeDescription":{"href":"https://github.com/microsoft/pyright/blob/main/docs/configuration.md#reportAssignmentType"},"source":"Pyright","message":"Type \"Literal['s']\" is not assignable to declared type \"int\""}`
	b.push("a.py", "1", diag)
	b.await(t, "published", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 1 })
	d := b.sets(t)[0].Diagnostics[0]
	if d.Freshness != "current" || d.Range == nil || d.Basis.Version == nil || *d.Basis.Version != 1 || d.Source != "pyright/Pyright" || d.Code != "reportAssignmentType" || d.Severity != "error" {
		t.Fatalf("%+v", d)
	}
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 2, "x: int = 2\ny: int = \"s\"\n"); err != nil {
		t.Fatal(err)
	}
	b.push("a.py", "1", ``) // stale
	b.push("a.py", "", diag)
	// An unversioned push (Pyright's close/untrack cleanup shape) is uncertain
	// and, under the shared normalizer's current rule, unpositioned.
	b.await(t, "unversioned", func(r []ResultSet) bool {
		return len(r) == 1 && len(r[0].Diagnostics) == 1 && r[0].Diagnostics[0].Freshness == "uncertain" && r[0].Diagnostics[0].Basis.Version == nil && r[0].Diagnostics[0].Range == nil
	})
	b.push("pyproject.toml", "", errAt)
	b.push("n.ipynb", "1", errAt)
	b.push("b.py", "1", errAt) // not opened
	b.push("a.py", "2", ``)
	b.await(t, "cleared", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 0 })
	if got := b.sets(t); len(got) != 1 || got[0].Scope.Path != "a.py" {
		t.Fatalf("%+v", got)
	}
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///etc/hosts","version":1,"diagnostics":[{"message":"leak"}]}}`)
	if n := <-b.notices; n != OutsideRootNotice {
		t.Fatal(n)
	}
	b.srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"http://x/a.py","diagnostics":[]}}`)
	if n := <-b.notices; n != partialPyrightNotice {
		t.Fatal(n)
	}
	// Ruff's notification for the same file is not Pyright's, and vice versa.
	bridge := NewPyrightBridge(b.root, b.h.Manager)
	for _, n := range []SessionNotification{
		{Project: b.root.Project(), ProviderID: ruffProviderID, Method: "textDocument/publishDiagnostics", Params: json.RawMessage(`{"uri":"` + uriOf(b.root, "a.py") + `","version":2,"diagnostics":[` + errAt + `]}`)},
		{Project: Project{HostID: "x", Root: "/x"}, ProviderID: pyrightProviderID, Method: "textDocument/publishDiagnostics"},
		{Project: b.root.Project(), ProviderID: pyrightProviderID, Method: "window/showMessage"},
	} {
		bridge.Handle(n)
	}
	if got := b.sets(t); len(got) != 1 || len(got[0].Diagnostics) != 0 {
		t.Fatalf("%+v", got)
	}
}

// TestPyrightRealSmoke runs a real, already installed Pyright against a
// temporary project. Opt-in: CD_PYRIGHT_SMOKE_PATH is a colon-separated list
// of reviewed directories holding pyright-langserver and node. It logs whether
// the observed pushes carry a document version; it never installs anything.
func TestPyrightRealSmoke(t *testing.T) {
	dirs := os.Getenv("CD_PYRIGHT_SMOKE_PATH")
	if dirs == "" {
		t.Skip("set CD_PYRIGHT_SMOKE_PATH to run a real Pyright")
	}
	home, _ := os.UserHomeDir()
	root := goplsRoot(t, map[string]string{"a.py": "x", "server": "synthetic server", "cfg": "synthetic cfg"})
	p, err := DiscoverPyright(root, filepath.SplitList(dirs), PyrightEnvironment{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := p.Server()
	if !ok {
		t.Fatalf("%s: %s", p.Availability(), p.Provider().Notice)
	}
	t.Logf("pyright %s on node %s, policy %s", p.Version(), p.NodeVersion(), cfg.AdapterPolicy)
	versions := make(chan bool, 64)
	var bridge *PyrightBridge
	trust := NewTrust(nil)
	sessions := NewSessionManager(trust, SessionOptions{Launch: HostSessionLaunch, InitTimeout: time.Minute, Notifications: func(n SessionNotification) {
		var v struct{ Version *int }
		_ = json.Unmarshal(n.Params, &v)
		select {
		case versions <- v.Version != nil:
		default:
		}
		bridge.Handle(n)
	}})
	t.Cleanup(sessions.Close)
	fp, err := Fingerprint(root, cfg.Tools())
	if err != nil {
		t.Fatal(err)
	}
	approve(t, trust, root, cfg.Tools(), fp, "session")
	h := NewManagerForTest(t, root)
	bridge = NewPyrightBridge(root, h.Manager)
	bridge.Notice = func(s string) { t.Log("notice:", s) }
	h.SetDocumentProvider(SessionDocuments{Sessions: sessions, Resolve: func(r AuthorizedRoot, path string) (DocumentServer, bool) {
		lang, ok := PythonLanguageID(path)
		return DocumentServer{Config: cfg, LanguageID: lang}, ok
	}})
	ctx, c := context.Background(), h.snap.Context
	if _, err := sessions.Attach(ctx, root, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := h.OpenDocument(ctx, c, "a.py", "d1", 1, "x: int = \"s\"\n", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if s, err := h.GetSnapshot(c); err == nil && len(s.ResultSets) == 1 && len(s.ResultSets[0].Diagnostics) > 0 {
			for _, d := range s.ResultSets[0].Diagnostics {
				t.Logf("%s %s %s %s basis=%+v", d.Source, d.Code, d.Severity, d.Freshness, d.Basis)
			}
			for {
				select {
				case v := <-versions:
					t.Logf("push versioned: %v", v)
				default:
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no Pyright diagnostics within a minute")
}
