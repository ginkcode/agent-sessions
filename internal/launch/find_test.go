package launch

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeHome is a Windows-like user profile under a temp dir.
type fakeHome struct {
	t                       *testing.T
	profile, roaming, local string
	pathHits                map[string]string
}

func newFakeHome(t *testing.T) *fakeHome {
	root := t.TempDir()
	h := &fakeHome{
		t:        t,
		profile:  filepath.Join(root, "Users", "me"),
		roaming:  filepath.Join(root, "Users", "me", "AppData", "Roaming"),
		local:    filepath.Join(root, "Users", "me", "AppData", "Local"),
		pathHits: map[string]string{},
	}
	return h
}

func (h *fakeHome) file(path string, age time.Duration) string {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("exe"), 0o755); err != nil {
		h.t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(path, mt, mt); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *fakeHome) finder(goarch string) finder {
	env := map[string]string{"USERPROFILE": h.profile, "APPDATA": h.roaming, "LOCALAPPDATA": h.local}
	return finder{
		goos:   "windows",
		goarch: goarch,
		getenv: func(k string) string { return env[k] },
		lookPath: func(name string) (string, error) {
			if p, ok := h.pathHits[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
	}
}

func TestFindWindowsBundledCLIs(t *testing.T) {
	h := newFakeHome(t)
	h.file(filepath.Join(h.local, "OpenAI", "Codex", "bin", "aaaa", "codex.exe"), 48*time.Hour)
	codexNew := h.file(filepath.Join(h.local, "OpenAI", "Codex", "bin", "bbbb", "codex.exe"), time.Hour)
	h.file(filepath.Join(h.local, "OpenAI", "Codex", "bin", "cccc", "rg.exe"), 0)
	openc := h.file(filepath.Join(h.local, "Programs", "@opencodedesktop", "resources", "opencode-cli.exe"), time.Hour)
	claude := h.file(filepath.Join(h.local, "Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude", "claude-code", "2.1.286", "claude.exe"), time.Hour)
	f := h.finder("amd64")

	for name, want := range map[string]string{"codex": codexNew, "opencode": openc, "claude": claude} {
		got, err := f.find(name)
		if err != nil || got.Path != want || got.OnPath {
			t.Errorf("find(%s) = %+v, %v; want %s", name, got, err, want)
		}
		var args []string
		if name == "codex" {
			args = []string{"-c", "features.daemon_auto_start=false"}
		}
		if !slices.Equal(got.Args, args) {
			t.Errorf("find(%s) args = %q, want %q", name, got.Args, args)
		}
	}
}

func TestFindWindowsOrder(t *testing.T) {
	h := newFakeHome(t)
	f := h.finder("amd64")

	ext := h.file(filepath.Join(h.profile, ".vscode", "extensions", "anthropic.claude-code-2.1.288-win32-x64", "resources", "native-binary", "claude.exe"), 0)
	if got, _ := f.find("claude"); got.Path != ext {
		t.Fatalf("extension only: got %q", got.Path)
	}
	desktop := h.file(filepath.Join(h.roaming, "Claude", "claude-code", "2.1.286", "claude.exe"), time.Hour)
	if got, _ := f.find("claude"); got.Path != desktop {
		t.Fatalf("desktop beats extension: got %q", got.Path)
	}
	native := h.file(filepath.Join(h.profile, ".local", "bin", "claude.exe"), 24*time.Hour)
	if got, _ := f.find("claude"); got.Path != native {
		t.Fatalf("standalone beats desktop: got %q", got.Path)
	}
	onPath := h.file(filepath.Join(h.roaming, "npm", "claude.cmd"), 0)
	h.pathHits["claude"] = onPath
	if got, _ := f.find("claude"); got.Path != onPath || !got.OnPath {
		t.Fatalf("PATH wins: got %+v", got)
	}
}

func TestFindSkipsClaudeDesktopAlias(t *testing.T) {
	h := newFakeHome(t)
	f := h.finder("amd64")
	h.pathHits["claude"] = filepath.Join(h.local, "Microsoft", "WindowsApps", "Claude.exe")
	native := h.file(filepath.Join(h.profile, ".local", "bin", "claude.exe"), 0)
	if got, err := f.find("claude"); err != nil || got.Path != native || got.OnPath {
		t.Fatalf("alias not skipped: %+v, %v", got, err)
	}
	// Only Claude Desktop installs that alias.
	h.pathHits["codex"] = filepath.Join(h.local, "Microsoft", "WindowsApps", "codex.exe")
	if got, err := f.find("codex"); err != nil || !got.OnPath {
		t.Fatalf("codex alias: %+v, %v", got, err)
	}
}

func TestFindCodexExtensionArch(t *testing.T) {
	h := newFakeHome(t)
	extDir := filepath.Join(h.profile, ".cursor", "extensions", "openai.chatgpt-26.930.41038-win32-arm64", "bin")
	h.file(filepath.Join(extDir, "windows-x86_64", "codex.exe"), 0)
	arm := h.file(filepath.Join(extDir, "windows-aarch64", "codex.exe"), 0)
	if got, _ := h.finder("arm64").find("codex"); got.Path != arm || !slices.Equal(got.Args, codexBundleArgs) {
		t.Fatalf("arm64: got %q", got.Path)
	}
	if got, _ := h.finder("amd64").find("codex"); got.Path != filepath.Join(extDir, "windows-x86_64", "codex.exe") || !slices.Equal(got.Args, codexBundleArgs) {
		t.Fatalf("amd64: got %q", got.Path)
	}
}

func TestFindCodexBundleArgs(t *testing.T) {
	for _, editor := range editorDirs {
		t.Run(editor, func(t *testing.T) {
			h := newFakeHome(t)
			ext := h.file(filepath.Join(h.profile, editor, "extensions", "openai.chatgpt-26.930.41038-win32-x64", "bin", "windows-x86_64", "codex.exe"), 0)
			f := h.finder("amd64")
			got, err := f.find("codex")
			if err != nil || got.Path != ext || got.OnPath || !slices.Equal(got.Args, codexBundleArgs) {
				t.Fatalf("extension: %+v, %v", got, err)
			}
			// These defaults also apply if the same bundled CLI is on PATH.
			h.pathHits["codex"] = ext
			got, err = f.find("codex")
			if err != nil || got.Path != ext || !got.OnPath || !slices.Equal(got.Args, codexBundleArgs) {
				t.Fatalf("extension on PATH: %+v, %v", got, err)
			}
			// A caller cannot change the defaults subsequent lookups return.
			got.Args[0] = "changed"
			if again, _ := f.find("codex"); !slices.Equal(again.Args, codexBundleArgs) {
				t.Errorf("shared defaults: %q", again.Args)
			}
			// TEMP on a Windows runner may use 8.3 names while PATH uses
			// the long form. Both must identify the same bundled CLI.
			resolved, err := filepath.EvalSymlinks(ext)
			if err != nil {
				t.Fatal(err)
			}
			h.pathHits["codex"] = resolved
			if again, _ := f.find("codex"); !slices.Equal(again.Args, codexBundleArgs) {
				t.Errorf("resolved bundle path not recognized: %+v", again)
			}
			h.pathHits["codex"] = strings.ToUpper(resolved)
			if again, _ := f.find("codex"); !slices.Equal(again.Args, codexBundleArgs) {
				t.Errorf("case-insensitive bundle path not recognized: %+v", again)
			}
		})
	}

	h := newFakeHome(t)
	f := h.finder("amd64")
	desktop := h.file(filepath.Join(h.local, "OpenAI", "Codex", "bin", "hash", "codex.exe"), 0)
	h.pathHits["codex"] = desktop
	if got, err := f.find("codex"); err != nil || !got.OnPath || !slices.Equal(got.Args, codexBundleArgs) {
		t.Errorf("desktop on PATH: %+v, %v", got, err)
	}
	f.goos = "linux"
	if got, err := f.find("codex"); err != nil || len(got.Args) != 0 {
		t.Errorf("Linux PATH: %+v, %v", got, err)
	}
	f.goos = "windows"
	delete(h.pathHits, "codex")
	npm := h.file(filepath.Join(h.roaming, "npm", "codex.cmd"), 0)
	if got, err := f.find("codex"); err != nil || got.Path != npm || len(got.Args) != 0 {
		t.Errorf("npm: %+v, %v", got, err)
	}
	for _, path := range []string{npm, filepath.Join(h.profile, "bin", "codex.exe")} {
		h.pathHits["codex"] = path
		if got, err := f.find("codex"); err != nil || got.Path != path || !got.OnPath || len(got.Args) != 0 {
			t.Errorf("other PATH CLI: %+v, %v", got, err)
		}
	}
}

func TestFindNotFound(t *testing.T) {
	h := newFakeHome(t)
	// A directory named like the CLI is not an executable.
	if err := os.MkdirAll(filepath.Join(h.profile, ".local", "bin", "claude.exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := h.finder("amd64").find("claude")
	if !errors.Is(err, ErrAgentNotFound) || !strings.Contains(err.Error(), "Claude Desktop") {
		t.Fatalf("err = %v", err)
	}

	// Other platforms only use PATH.
	f := h.finder("amd64")
	f.goos = "linux"
	h.file(filepath.Join(h.local, "OpenAI", "Codex", "bin", "x", "codex.exe"), 0)
	if _, err := f.find("codex"); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("linux searched Windows locations: %v", err)
	}
}

func TestExpandKeepsGlobCharsInBase(t *testing.T) {
	h := newFakeHome(t)
	name := "we[ir]d*"
	if runtime.GOOS == "windows" {
		name = "we[ir]d" // Windows file names cannot contain *
	}
	base := filepath.Join(h.profile, name)
	want := h.file(filepath.Join(base, "v1", "x.exe"), 0)
	got := expand(base, []string{"*", "x.exe"})
	if len(got) != 1 || got[0] != want {
		t.Fatalf("expand = %q, want %q", got, want)
	}
}
