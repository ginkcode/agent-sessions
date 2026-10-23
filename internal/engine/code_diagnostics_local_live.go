package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

// The production local live route (CD-22, experimental). Discovery reads
// metadata only: executable lookup in absolute host directories, gopls build
// info, the toolchain root derived from the resolved go executable, and vtsls,
// TypeScript and Node package/header metadata. It never
// runs a version probe, installs, downloads or reads a project-controlled PATH.
// Execution remains gated per project by the composite approval fingerprint;
// the launcher is reached only through SessionManager after that gate.
//
// An SSH or WSL host runs the same route inside its headless server
// (WithRemoteCodeDiagnostics), next to the files, as editors do.

// localLiveSupported reports the platforms with host identity and process-group
// ownership of server trees: macOS and Linux (including WSL), any architecture.
func localLiveSupported() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" }

// localExecutableDirs returns the absolute, de-duplicated search directories:
// the app's inherited PATH first, then the user's login shell PATH (a GUI app
// launched from Finder does not inherit it), then conventional per-user and
// system install locations. Relative or parent-escaping entries are dropped;
// the list is bounded.
func localExecutableDirs(home string) []string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	dirs = append(dirs, loginShellPath(home)...)
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "go", "bin"), filepath.Join(home, ".local", "bin"),
			// mise's default per-user Go install ("latest" is mise's own link).
			// Finder-launched apps never see mise's shell activation PATH, and
			// its shims resolve to the mise launcher, not a Go toolchain.
			filepath.Join(home, ".local", "share", "mise", "installs", "go", "latest", "bin"),
			// Likewise mise's Node; its bin also holds global npm commands.
			filepath.Join(home, ".local", "share", "mise", "installs", "node", "latest", "bin"))
		for _, p := range filepath.SplitList(os.Getenv("GOPATH")) {
			if filepath.IsAbs(p) {
				dirs = append(dirs, filepath.Join(p, "bin"))
			}
		}
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/go/bin", "/usr/local/bin")
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "" || !filepath.IsAbs(d) || filepath.Clean(d) != d || slices.Contains(out, d) {
			continue
		}
		out = append(out, d)
		if len(out) == 64 {
			break
		}
	}
	return out
}

// localGoRoot derives GOROOT from the first resolved go executable, exactly as
// the go vet checker does. Empty when no go executable is installed.
func localGoRoot(dirs []string) string {
	for _, dir := range dirs {
		real, err := filepath.EvalSymlinks(filepath.Join(dir, "go"))
		if err != nil {
			continue
		}
		st, err := os.Stat(real)
		if err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 && filepath.Base(filepath.Dir(real)) == "bin" {
			return filepath.Dir(filepath.Dir(real))
		}
	}
	return ""
}

// hostGoRoot is the Go toolchain a managed gopls is built with: the one the
// go vet checker and gopls discovery use.
func (e *Engine) hostGoRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return localGoRoot(localExecutableDirs(home))
}

// hostNode is the Node.js the TypeScript server would run on; a managed
// vtsls install uses it instead of downloading one.
func hostNode() (prefix, version string) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", ""
	}
	return cd.HostNode(localExecutableDirs(home))
}

// discoverLocalLive is the production discovery function for the local route.
// gopls is searched next to the selected toolchain first so a matching install
// wins over an unrelated one earlier on PATH. vtsls and node use the plain
// reviewed directories; node is taken from a global vtsls install's prefix
// when there is one. ruff uses the same directories (uv tool and pipx link
// into ~/.local/bin). App-managed installs come last, after the project's
// and the user's own copies.
func (e *Engine) discoverLocalLive(root cd.AuthorizedRoot) ([]cd.Registration, error) {
	return discoverLocalLiveWith(root, e.managedDirs())
}

func discoverLocalLiveWith(root cd.AuthorizedRoot, managed []string) ([]cd.Registration, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, diagnosticError("unsupported", "Home directory unavailable")
	}
	cache, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(cache) {
		return nil, diagnosticError("unsupported", "Cache directory unavailable")
	}
	// Managed installs keep their place within the search budget.
	dirs := localExecutableDirs(home)
	dirs = append(dirs[:min(len(dirs), 64-len(managed))], managed...)
	goroot := localGoRoot(dirs)
	search := dirs
	if goroot != "" {
		search = append([]string{filepath.Join(goroot, "bin")}, slices.DeleteFunc(slices.Clone(dirs), func(d string) bool { return d == filepath.Join(goroot, "bin") })...)
		if len(search) > 64 {
			search = search[:64]
		}
	}
	return cd.DiscoverLocalLive(root, cd.LocalLiveEnvironment{
		GoPath: search, Path: dirs,
		Go:      cd.GoplsEnvironment{Home: home, Cache: filepath.Join(cache, "go-build"), ModuleCache: filepath.Join(home, "go", "pkg", "mod"), GoRoot: goroot},
		TS:      cd.TSEnvironment{Home: home},
		Ruff:    cd.RuffEnvironment{Home: home},
		Pyright: cd.PyrightEnvironment{Home: home},
	})
}
