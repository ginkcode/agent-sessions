package codediagnostics

// SYNTHETIC fixtures: the fake binaries, wheel metadata and project trees below
// prove this adapter's handling only. They do not claim real-server behavior.
// Observed Ruff behavior is recorded in testdata/ruff/evidence.json; see
// docs/plan/code-diagnostics-python.md.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fakeRuffBinary = "\xcf\xfa\xed\xfe synthetic ruff"

// writeRuffWheel installs a SYNTHETIC ruff wheel into prefix the way uv tool,
// pipx and pip do: bin/ruff plus lib/<python>/site-packages/ruff-*.dist-info
// whose RECORD lists ../../../bin/ruff with its hash. Nothing is executed.
func writeRuffWheel(t *testing.T, prefix, python, version, binary string) {
	t.Helper()
	sum := sha256.Sum256([]byte(binary))
	site, dist := "lib/"+python+"/site-packages/", "ruff-"+version+".dist-info/"
	writeTree(t, prefix, map[string]string{
		"bin/ruff":                binary,
		site + dist + "METADATA":  "Metadata-Version: 2.4\nName: ruff\nVersion: " + version + "\nSummary: x\n\nName: evil\n",
		site + dist + "RECORD":    "../../../bin/ruff,sha256=" + base64.RawURLEncoding.EncodeToString(sum[:]) + ",31\n" + dist + "METADATA,,\n",
		site + dist + "INSTALLER": "uv\n",
		site + "ruff/__init__.py": "",
	})
	if err := os.Chmod(filepath.Join(prefix, "bin", "ruff"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// ruffHost builds a SYNTHETIC uv tool install: <home>/.local/bin/ruff links to
// <home>/.local/share/uv/tools/ruff/bin/ruff at the verified version.
func ruffHost(t *testing.T, version string) ([]string, RuffEnvironment, string) {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	tool := filepath.Join(home, ".local/share/uv/tools/ruff")
	writeRuffWheel(t, tool, "python3.13", version, fakeRuffBinary)
	bin := filepath.Join(home, ".local/bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tool, "bin/ruff"), filepath.Join(bin, "ruff")); err != nil {
		t.Fatal(err)
	}
	return []string{bin}, RuffEnvironment{Home: home}, filepath.Join(tool, "bin/ruff")
}

func TestPythonLanguageOwnership(t *testing.T) {
	for _, f := range []string{"a.py", "pkg/a.pyi", "A.PY"} {
		if lang, ok := PythonLanguageID(f); !ok || lang != "python" {
			t.Errorf("%s: %q %v", f, lang, ok)
		}
	}
	for _, f := range []string{"a.ipynb", "a.pyc", "pyproject.toml", "a.py.bak", "py"} {
		if _, ok := PythonLanguageID(f); ok {
			t.Errorf("%s must not be owned", f)
		}
	}
}

func TestRuffProjectDiscovery(t *testing.T) {
	cases := map[string]struct {
		files             map[string]string
		projects, configs []string
		partial, sources  bool
	}{
		"pyproject":            {map[string]string{"pyproject.toml": "", "a.py": ""}, []string{""}, []string{"pyproject.toml"}, false, true},
		"ruff configs bound":   {map[string]string{"ruff.toml": "", "sub/.ruff.toml": ""}, []string{"", "sub"}, []string{"ruff.toml", "sub/.ruff.toml"}, false, false},
		"no plain markers":     {map[string]string{"requirements.txt": "", "b/setup.py": "", "c/setup.cfg": ""}, nil, nil, false, true},
		"nested no promotion":  {map[string]string{"svc/pyproject.toml": "", "svc/a.py": "", "x.py": ""}, []string{"svc"}, []string{"svc/pyproject.toml"}, false, true},
		"environments skipped": {map[string]string{"pyproject.toml": "", ".venv/lib/python3.13/site-packages/x/pyproject.toml": "", ".venv/lib/x.py": "", "venv/pyproject.toml": "", "__pycache__/ruff.toml": ""}, []string{""}, []string{"pyproject.toml"}, false, false},
		"hidden skipped":       {map[string]string{"pyproject.toml": "", ".cache/ruff.toml": ""}, []string{""}, []string{"pyproject.toml"}, true, false},
		"sources only":         {map[string]string{"a.py": "", "package.json": "{}"}, nil, nil, false, true},
		"no python":            {map[string]string{"a.ts": "", "package.json": "{}"}, nil, nil, false, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, err := discoverProjectDirs(goplsRoot(t, c.files), ruffProjectMarkers)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(w.packages, c.projects) || !slices.Equal(w.configs, c.configs) || w.partial != c.partial || w.sources != c.sources {
				t.Fatalf("%+v", w)
			}
		})
	}
}

func TestDiscoverRuffAvailability(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"pyproject.toml": "[tool.ruff]\n", "pkg/ruff.toml": "", "a.py": ""})
	path, env, real := ruffHost(t, "0.15.20")
	p, err := DiscoverRuff(root, path, env)
	if err != nil || p.Availability() != "available" || p.ProjectLocal() || p.Version() != "0.15.20" {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	cfg, ok := p.Server()
	// The wheel's own binary, never the PATH link, a shell or uvx.
	if !ok || cfg.ProviderID != ruffProviderID || cfg.Executable != real || !slices.Equal(cfg.Args, []string{"server"}) ||
		!slices.Equal(cfg.Env, []string{"HOME=" + env.Home, "PATH=/usr/bin:/bin"}) || cfg.Toolchain != nil || cfg.Settings != nil || cfg.DeclareDiagnostics ||
		!slices.Equal(cfg.ConfigPaths, []string{"pkg/ruff.toml", "pyproject.toml"}) || cfg.command().AdapterPolicy != ruffPolicy {
		t.Fatalf("%+v %s", cfg, cfg.Policy())
	}
	if err := validateCommand(root, cfg.Tools(), cfg.command()); err != nil {
		t.Fatal(err)
	}
	st := p.Provider()
	// The whole project is covered; configurations are bound, not boundaries.
	if st.Coverage.Kind != "openedDocuments" || st.Coverage.Partial || !slices.Equal(st.Coverage.Roots, []string{""}) ||
		!slices.Equal(st.DocumentExtensions, []string{".py", ".pyi"}) ||
		!strings.Contains(st.Notice, "not type errors") || !strings.Contains(st.Notice, "not part of approval") || !strings.Contains(st.Notice, "opened documents only") {
		t.Fatalf("%+v", st)
	}
	// A changed binary or project configuration changes the fingerprint.
	before, _ := Fingerprint(root, cfg.Tools())
	writeTree(t, root.Project().Root, map[string]string{"pyproject.toml": "[tool.ruff]\nline-length = 10\n"})
	if after, err := Fingerprint(root, cfg.Tools()); err != nil || after == before {
		t.Fatal("config change kept the fingerprint", err)
	}

	// Independent unavailability: never a server, never a process.
	host := func(mutate func(prefix string)) []string {
		path, _, real := ruffHost(t, "0.15.20")
		mutate(filepath.Dir(filepath.Dir(real)))
		return path
	}
	dist := "lib/python3.13/site-packages/ruff-0.15.20.dist-info"
	standalone, shim := t.TempDir(), t.TempDir()
	writeTree(t, standalone, map[string]string{"ruff": fakeRuffBinary})
	_ = os.Chmod(filepath.Join(standalone, "ruff"), 0o755)
	writeTree(t, shim, map[string]string{"ruff": "#!/bin/sh\nexec mise x -- ruff \"$@\"\n"})
	_ = os.Chmod(filepath.Join(shim, "ruff"), 0o755)
	wrong, _, _ := ruffHost(t, "0.14.0")
	// Any native ruff binary is used, as editors do; the version is shown only
	// when wheel metadata vouches for it.
	for name, tc := range map[string]struct {
		path                   []string
		state, notice, version string
	}{
		"missing":             {[]string{t.TempDir()}, "missingTool", "uv tool install ruff", ""},
		"relative path":       {[]string{"bin", "../bin"}, "missingTool", "Ruff is not installed", ""},
		"standalone binary":   {[]string{standalone}, "available", "", ""},
		"shim":                {[]string{shim}, "unsupported", "script or version-manager shim", ""},
		"shim before install": {append([]string{shim, standalone}, path...), "available", "", ""},
		"other version":       {wrong, "available", "", "0.14.0"},
		"hostile version": {host(func(p string) {
			writeTree(t, p, map[string]string{dist + "/METADATA": "Name: ruff\nVersion: 0.15.20<b>\n"})
		}), "available", "", ""},
		"binary modified":          {host(func(p string) { writeTree(t, p, map[string]string{"bin/ruff": fakeRuffBinary + "x"}) }), "available", "", ""},
		"modified before verified": {append(host(func(p string) { writeTree(t, p, map[string]string{"bin/ruff": "changed"}) }), path...), "available", "", ""},
		"record missing":           {host(func(p string) { _ = os.Remove(filepath.Join(p, dist, "RECORD")) }), "available", "", ""},
		"foreign package": {host(func(p string) {
			writeTree(t, p, map[string]string{dist + "/METADATA": "Name: evil\nVersion: 0.15.20\n"})
		}), "available", "", ""},
		"renamed binary": {host(func(p string) {
			_ = os.Rename(filepath.Join(p, "bin/ruff"), filepath.Join(p, "bin/other"))
			_ = os.Symlink("other", filepath.Join(p, "bin/ruff"))
		}), "unsupported", "not the Ruff program", ""},
	} {
		p, err := DiscoverRuff(root, tc.path, env)
		if err != nil || p.Availability() != tc.state || !strings.Contains(p.Provider().Notice, tc.notice) || p.Version() != tc.version {
			t.Errorf("%s: %v %s %q %q", name, err, p.Availability(), p.Version(), p.Provider().Notice)
			continue
		}
		if tc.state == "available" {
			continue
		}
		if _, ok := p.Server(); ok || !p.Provider().Coverage.Partial {
			t.Errorf("%s: unavailable plan offers a server or full coverage", name)
		}
	}
	// Any Python file is enough; without one Ruff is a muted unsupported,
	// never a request to install.
	if p, _ = DiscoverRuff(goplsRoot(t, map[string]string{"a.py": ""}), path, env); p.Availability() != "available" {
		t.Fatal("plain Python file", p.Availability())
	}
	for _, path := range [][]string{path, {t.TempDir()}} {
		if p, _ = DiscoverRuff(goplsRoot(t, map[string]string{"a.ts": "", "requirements.txt": ""}), path, env); p.Availability() != "unsupported" || !strings.Contains(p.Provider().Notice, "No Python file") {
			t.Fatal("no Python", p.Availability())
		}
	}
	for _, bad := range []RuffEnvironment{{}, {Home: "relative"}, {Home: "/x/../y"}} {
		if _, err := DiscoverRuff(root, path, bad); errCode(err) != "invalidArgument" {
			t.Fatal(err)
		}
	}
}

// A project virtual environment (uv sync, pip install into .venv) is executable
// project content, preferred over the reviewed path; a .venv link escaping the
// root is not.
func TestRuffProjectVirtualEnvironment(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"pyproject.toml": "", "src/a.py": ""})
	venv := filepath.Join(root.Project().Root, ".venv")
	writeRuffWheel(t, venv, "python3.12", "0.15.20", fakeRuffBinary+" venv")
	path, env, _ := ruffHost(t, "0.15.20")
	p, err := DiscoverRuff(root, path, env)
	if err != nil || p.Availability() != "available" || !p.ProjectLocal() {
		t.Fatalf("%v %s %q", err, p.Availability(), p.Provider().Notice)
	}
	if cfg, _ := p.Server(); cfg.Executable != filepath.Join(venv, "bin/ruff") || !slices.Equal(cfg.ConfigPaths, []string{"pyproject.toml"}) {
		t.Fatalf("%+v", cfg)
	}
	// The project's own copy wins whatever its version, as in editors.
	_ = os.RemoveAll(venv)
	writeRuffWheel(t, venv, "python3.12", "0.1.0", fakeRuffBinary+" venv")
	if p, _ = DiscoverRuff(root, path, env); p.Availability() != "available" || !p.ProjectLocal() || p.Version() != "0.1.0" {
		t.Fatalf("%s %q", p.Availability(), p.Provider().Notice)
	}
	_ = os.RemoveAll(venv)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	writeRuffWheel(t, outside, "python3.12", "0.15.20", fakeRuffBinary)
	if err := os.Symlink(outside, venv); err != nil {
		t.Fatal(err)
	}
	if _, _, local, _ := resolveRuff(root, nil); local {
		t.Fatal("escaped .venv treated as project content")
	}
}

func TestRuffDocumentResolutionThroughLocalLive(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"svc/pyproject.toml": "", "svc/app/a.py": "", "svc/a.pyi": "", "x.py": "", "svc/n.ipynb": "", "svc/b.ts": ""})
	path, env, _ := ruffHost(t, "0.15.20")
	_, goEnv := goplsHost(t, "v0.24.0", goplsModulePath)
	tsPath, tsEnv := tsHost(t)
	regs, err := DiscoverLocalLive(root, LocalLiveEnvironment{Path: append(path, tsPath...), Go: goEnv, TS: tsEnv, Ruff: env, Pyright: PyrightEnvironment{Home: tsEnv.Home, NodeDir: tsEnv.NodeDir}})
	if err != nil || len(regs) != 5 || regs[3].ProviderID != ruffProviderID || regs[3].Availability != "available" {
		t.Fatal(err, regs)
	}
	// gopls is unaffected (no Go module); the TypeScript file enables vtsls.
	if regs[0].Availability == "available" || regs[1].Availability != "available" {
		t.Fatalf("%s %s", regs[0].Availability, regs[1].Availability)
	}
	docs, err := DiscoveryDocuments(root, regs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ruff covers the whole project, not only the pyproject.toml directory.
	for file, want := range map[string]string{"svc/app/a.py": ruffProviderID, "svc/a.pyi": ruffProviderID, "x.py": ruffProviderID, "svc/n.ipynb": "", "svc/b.ts": tsProviderID} {
		server, ok := docs.Resolve(root, file)
		if ok != (want != "") || (ok && server.Config.ProviderID != want) {
			t.Errorf("%s: %v %+v", file, ok, server)
		}
	}
}

type ruffBridgeEnv struct{ *bridgeEnv }

func newRuffBridgeEnv(t *testing.T, files map[string]string) *ruffBridgeEnv {
	t.Helper()
	b := &ruffBridgeEnv{&bridgeEnv{notices: make(chan string, 64)}}
	var bridge *RuffBridge
	e := newEnv(t, SessionOptions{Notifications: func(n SessionNotification) { bridge.Handle(n) }})
	b.sessionEnv = e
	b.root, b.cfg = e.project()
	b.cfg.ProviderID = ruffProviderID
	e.approve(b.root, b.cfg)
	writeTree(t, b.root.Project().Root, files)
	b.h = NewManagerForTest(t, b.root)
	bridge = NewRuffBridge(b.root, b.h.Manager)
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

// Synthetic fake server sending what the observed Ruff sends: versioned pushes
// with string codes and multi-line messages are current and positioned; an
// empty push clears; config files, other providers and stale versions never
// create state.
func TestRuffBridgeVersionedPushes(t *testing.T) {
	b := newRuffBridgeEnv(t, map[string]string{"a.py": "x", "pyproject.toml": ""})
	ctx, c := context.Background(), b.h.snap.Context
	if _, err := b.h.OpenDocument(ctx, c, "a.py", "d1", 1, "import os\nx = (\n", ""); err != nil {
		t.Fatal(err)
	}
	b.push("a.py", "1", `{"range":{"start":{"line":0,"character":7},"end":{"line":0,"character":9}},"severity":2,"code":"F401","codeDescription":{"href":"https://docs.astral.sh/ruff/rules/unused-import"},"source":"Ruff","message":"`+"`os`"+` imported but unused\n\nhelp: Remove unused import: `+"`os`"+`","data":{"noqa_edit":{}}},`+
		`{"range":{"start":{"line":1,"character":5},"end":{"line":2,"character":0}},"severity":1,"code":"invalid-syntax","source":"Ruff","message":"Expected ')', found newline"}`)
	b.await(t, "published", func(r []ResultSet) bool { return len(r) == 1 && len(r[0].Diagnostics) == 2 })
	ds := b.sets(t)[0].Diagnostics
	codes := []string{}
	for _, d := range ds {
		if d.Freshness != "current" || d.Range == nil || d.Basis.Version == nil || *d.Basis.Version != 1 || !strings.HasPrefix(d.Source, "ruff") {
			t.Fatalf("%+v", d)
		}
		codes = append(codes, d.Code+"/"+d.Severity)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"F401/warning", "invalid-syntax/error"}) {
		t.Fatal(codes)
	}
	if _, err := b.h.ChangeDocument(ctx, c, "d1", 2, "x = 1\n"); err != nil {
		t.Fatal(err)
	}
	b.push("a.py", "1", errAt) // stale
	b.push("pyproject.toml", "", errAt)
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
	// Another provider's notification is not Ruff's.
	NewRuffBridge(b.root, b.h.Manager).Handle(SessionNotification{
		Project: b.root.Project(), ProviderID: tsProviderID, Method: "textDocument/publishDiagnostics",
		Params: json.RawMessage(`{"uri":"` + uriOf(b.root, "a.py") + `","version":2,"diagnostics":[` + errAt + `]}`),
	})
	if got := b.sets(t); len(got) != 1 || len(got[0].Diagnostics) != 0 {
		t.Fatalf("%+v", got)
	}
}

// TestRuffEvidenceReplay replays recorded Ruff publications through the shared
// normalizer. It proves normalization of observed payloads only.
func TestRuffEvidenceReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/ruff/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Versions  map[string]string
		Scenarios []struct {
			ID           string
			Publications []struct{ Params json.RawMessage }
		}
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Versions["ruff"] == "" {
		t.Fatalf("evidence names no ruff version: %v", ev.Versions)
	}
	root := goplsRoot(t, map[string]string{"a.py": "x", "b.py": "x", "pyproject.toml": ""})
	codes := []string{}
	for _, s := range ev.Scenarios {
		for _, p := range s.Publications {
			fixed := strings.ReplaceAll(string(p.Params), "file:///fixture/", uriOf(root, "")+"/")
			pub, err := normalizeLSPPublication(root, json.RawMessage(fixed), ruffProviderID)
			if err != nil {
				t.Fatalf("%s: %v", s.ID, err)
			}
			if pub.Version == nil {
				t.Fatalf("%s: observed Ruff pushes are versioned", s.ID)
			}
			for _, d := range pub.Diagnostics {
				if d.Source != "ruff/Ruff" || d.Range == nil {
					t.Fatalf("%+v", d)
				}
				// The UTF-16 column after a non-BMP character is kept as sent.
				if d.Code == "F401" && (d.Range.Start.Character != 17 || !strings.Contains(d.Message, "\n\nhelp:")) {
					t.Fatalf("%+v", d)
				}
				codes = append(codes, d.Code+"/"+d.Severity)
			}
		}
	}
	if want := []string{"invalid-syntax/error", "E702/warning", "E402/warning", "F401/warning", "E501/warning"}; !slices.Equal(codes, want) {
		t.Fatal(codes)
	}
}
