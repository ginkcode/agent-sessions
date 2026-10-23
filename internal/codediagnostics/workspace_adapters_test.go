package codediagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func workspaceTestRoot(t *testing.T, files map[string]string) AuthorizedRoot {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	writeTree(t, base, files)
	root, err := AuthorizeRoot("workspace-test", base, func(dir string) bool { return dir == base })
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func workspaceTestEnv(t *testing.T) LocalLiveEnvironment {
	t.Helper()
	unixTools(t)
	tsPath, ts := tsHost(t)
	pyPath, py, _ := pyrightHost(t)
	ruffPath, ruff, _ := ruffHost(t, "0.14.0")
	return LocalLiveEnvironment{Path: append(append(tsPath, pyPath...), ruffPath...), TS: ts, Pyright: py, Ruff: ruff}
}
func workspaceRegistration(t *testing.T, registrations []Registration, id string) *LiveRegistration {
	t.Helper()
	for _, r := range registrations {
		if r.ProviderID == id {
			return r.Live
		}
	}
	t.Fatal("missing provider", id)
	return nil
}

// unixTools skips a test whose synthetic tools rely on Unix executable bits.
// Host tool discovery feeds local live checks, which run on darwin and linux
// only; Windows projects are checked through WSL.
func unixTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("host tool discovery is Unix-only")
	}
}

func TestWorkspaceAdaptersIndependentChildren(t *testing.T) {
	root := workspaceTestRoot(t, map[string]string{"api/pyproject.toml": "[project]\nname='api'", "api/main.py": "", "web/package.json": "{}", "web/tsconfig.json": "{}", "web/app/page.tsx": "", "processing/requirements.txt": "", "processing/run.py": "", "loose.py": "", "loose.ts": "", "node_modules/ignored/package.json": "{}", "node_modules/ignored/a.py": ""})
	api, err := scopedDiscoveryRoot(root, "api")
	if err != nil {
		t.Fatal(err)
	}
	processing, err := scopedDiscoveryRoot(root, "processing")
	if err != nil {
		t.Fatal(err)
	}
	apiPython, _ := writeVenv(t, api, ".venv")
	processPython, _ := writeVenv(t, processing, ".venv")
	writeTree(t, root.Project().Root, map[string]string{"web/node_modules/typescript/package.json": pkgJSON("typescript", "5.8.0"), "web/node_modules/typescript/lib/tsserver.js": "synthetic", "web/node_modules/typescript/lib/_tsserver.js": "synthetic", "web/node_modules/typescript/lib/typescript.js": "synthetic"})
	regs, split, err := discoverWorkspaceLive(root, workspaceTestEnv(t))
	if err != nil || !split {
		t.Fatalf("split=%v err=%v", split, err)
	}
	for _, id := range []string{"ruff", "pyright"} {
		live := workspaceRegistration(t, regs, id)
		if len(live.Workspaces) != 3 {
			t.Fatalf("%s workspaces %+v", id, live)
		}
		for _, file := range []string{"api/main.py", "processing/run.py", "loose.py"} {
			n := 0
			for _, w := range live.Workspaces {
				if _, ok := w.Resolve(file); ok {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%s %s owners=%d", id, file, n)
			}
		}
	}
	py := workspaceRegistration(t, regs, "pyright")
	for _, w := range py.Workspaces {
		expected := ""
		switch w.Server.Directory {
		case "api":
			expected = apiPython
		case "processing":
			expected = processPython
		default:
			continue
		}
		if !strings.Contains(string(w.Server.Settings), expected) {
			t.Fatalf("wrong interpreter %s: %s", w.Server.Directory, w.Server.Settings)
		}
		if !slices.Contains(w.Server.ConfigPaths, w.Server.Directory+"/.venv/pyvenv.cfg") {
			t.Fatalf("untranslated configs %+v", w.Server.ConfigPaths)
		}
	}
	ts := workspaceRegistration(t, regs, "typescript")
	if len(ts.Workspaces) != 2 {
		t.Fatalf("TS %+v", ts)
	}
	for _, w := range ts.Workspaces {
		if w.Server.Directory == "web" && !strings.Contains(string(w.Server.Settings), "web/node_modules/typescript/lib") {
			t.Fatal("child SDK not selected")
		}
	}
	documents, err := DiscoveryDocuments(root, regs, NewSessionManager(nil, SessionOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	tools := []ToolInput{}
	for _, r := range regs {
		for _, w := range r.Live.Workspaces {
			tools = append(tools, w.Server.Tools()...)
		}
	}
	before, err := Fingerprint(root, tools)
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, root.Project().Root, map[string]string{"processing/.venv/pyvenv.cfg": "changed environment"})
	after, err := Fingerprint(root, tools)
	if err != nil || before == after {
		t.Fatalf("child config not bound: %v", err)
	}
	for _, file := range []string{"api/main.py", "processing/run.py"} {
		servers := documents.ResolveAll(root, file)
		if len(servers) != 2 {
			t.Fatalf("Python pair not resolved for %s: %+v", file, servers)
		}
	}
}

func TestWorkspaceAdaptersMissingChildDoesNotFallThrough(t *testing.T) {
	root := workspaceTestRoot(t, map[string]string{"a/pyproject.toml": "", "a/main.py": "", "b/requirements.txt": "", "b/main.py": "", "outside.py": ""})
	env := workspaceTestEnv(t)
	env.Path = nil
	// Only a has a native local Ruff; root fallback and b remain missing.
	writeTree(t, root.Project().Root, map[string]string{"a/.venv/bin/ruff": "native synthetic"})
	if err := os.Chmod(filepath.Join(root.Project().Root, "a/.venv/bin/ruff"), 0755); err != nil {
		t.Fatal(err)
	}
	regs, _, err := discoverWorkspaceLive(root, env)
	if err != nil {
		t.Fatal(err)
	}
	live := workspaceRegistration(t, regs, "ruff")
	if live.State.Availability != "available" || !live.State.MissingTool || !live.State.Coverage.Partial {
		t.Fatalf("state %+v", live.State)
	}
	if !slices.Equal(live.State.Coverage.Roots, []string{"a"}) {
		t.Fatalf("coverage %+v", live.State.Coverage)
	}
	if !strings.Contains(live.State.Notice, "b:") {
		t.Fatal(live.State.Notice)
	}
	for _, w := range live.Workspaces {
		if _, ok := w.Resolve("b/main.py"); ok {
			t.Fatal("missing sibling claimed")
		}
	}
}

func TestWorkspaceAdaptersBoundariesAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		dirs  []string
		split bool
	}{
		{"single", map[string]string{"package.json": "{}", "src/a.ts": "", "src/tsconfig.json": "{}"}, nil, false},
		{"npm-workspaces", map[string]string{"repo/package.json": "{\"workspaces\":[\"packages/*\"]}", "repo/packages/a/package.json": "{}", "repo/packages/a/tsconfig.json": "{}", "repo/packages/a/index.ts": "", "other/package.json": "{}", "other/index.js": ""}, []string{"other", "repo"}, true},
		{"independent-nested", map[string]string{"web/package.json": "{}", "web/src/tsconfig.json": "{}", "web/src/a.ts": "", "web/example/package.json": "{}", "web/example/b.ts": ""}, []string{"web", "web/example"}, true},
		// Manifest-only folders (and skipped dot-directories) start no instance.
		{"manifest-only", map[string]string{"web/package.json": "{}", "web/a.ts": "", "tools/vtsls/package.json": "{}", "tools/vtsls/package-lock.json": "{}", ".github/workflows/ci.yml": ""}, []string{"web"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workspaceTestRoot(t, tc.files)
			regs, split, err := discoverWorkspaceLive(root, workspaceTestEnv(t))
			if err != nil || split != tc.split {
				t.Fatalf("%v %v", split, err)
			}
			if !split {
				return
			}
			live := workspaceRegistration(t, regs, "typescript")
			var dirs []string
			for _, w := range live.Workspaces {
				dirs = append(dirs, w.Server.Directory)
			}
			if !slices.Equal(dirs, tc.dirs) {
				t.Fatalf("dirs %v", dirs)
			}
		})
	}
}

func TestWorkspaceAdaptersLimitsAndAuthorization(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < maxLiveWorkspaces+3; i++ {
		files[fmt.Sprintf("p%02d/requirements.txt", i)] = ""
		files[fmt.Sprintf("p%02d/main.py", i)] = ""
	}
	root := workspaceTestRoot(t, files)
	env := workspaceTestEnv(t)
	regs, _, err := discoverWorkspaceLive(root, env)
	if err != nil {
		t.Fatal(err)
	}
	live := workspaceRegistration(t, regs, "ruff")
	if len(live.Workspaces) != maxLiveWorkspaces || !live.State.Coverage.Partial {
		t.Fatalf("unbounded %+v", live)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root.Project().Root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := scopedDiscoveryRoot(root, "escape"); err == nil {
		t.Fatal("symlink escape accepted")
	}
	authorized := true
	parent, err := AuthorizeRoot(root.Project().HostID, root.Project().Root, func(string) bool { return authorized })
	if err != nil {
		t.Fatal(err)
	}
	child, err := scopedDiscoveryRoot(parent, "p00")
	if err != nil {
		t.Fatal(err)
	}
	authorized = false
	if child.Revalidate() == nil {
		t.Fatal("child lost parent authorization")
	}
}

func TestWorkspaceAdaptersNoticesNameEachMessageOnce(t *testing.T) {
	root := workspaceTestRoot(t, map[string]string{"a/pyproject.toml": "", "a/main.py": "", "b/requirements.txt": "", "b/main.py": ""})
	env := workspaceTestEnv(t)
	env.Path = nil
	regs, _, err := discoverWorkspaceLive(root, env)
	if err != nil {
		t.Fatal(err)
	}
	notice := workspaceRegistration(t, regs, "ruff").State.Notice
	if !strings.HasPrefix(notice, "a, b: ") || strings.Count(notice, "Ruff is not installed") != 1 {
		t.Fatalf("notice not grouped: %q", notice)
	}
}

// Workspace instances reference one shared runtime; it must count once toward
// the aggregate hashing budget (regression: 4+ TypeScript projects failed).
func TestFingerprintHashesSharedFilesOnce(t *testing.T) {
	root := workspaceTestRoot(t, map[string]string{"a/pyproject.toml": ""})
	runtime := filepath.Join(t.TempDir(), "node")
	f, err := os.Create(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(200 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var tools []ToolInput
	for i := range 4 {
		tools = append(tools, ToolInput{ProviderID: fmt.Sprintf("tool-%d", i), Executable: runtime, ConfigPaths: []string{"a/pyproject.toml"}, Policy: "p"})
	}
	shared, err := Fingerprint(root, tools)
	if err != nil {
		t.Fatalf("shared runtime exceeded budget: %v", err)
	}
	if again, err := Fingerprint(root, tools); err != nil || again != shared {
		t.Fatalf("unstable fingerprint: %v", err)
	}
	writeTree(t, root.Project().Root, map[string]string{"a/pyproject.toml": "changed"})
	if changed, err := Fingerprint(root, tools); err != nil || changed == shared {
		t.Fatalf("shared config not bound: %v", err)
	}
}
