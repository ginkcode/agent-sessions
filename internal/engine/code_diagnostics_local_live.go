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
// Remote hosts never use this route: a headless server's Engine is constructed
// by `agent-sessions-cli serve` and stays fail-closed through rpc
// initializeDiagnostics (verified SSH/WSL identity required, CD-09 blocked).

// localLiveSupported reports the platforms where server behavior, host identity
// and process-tree ownership have been verified together.
func localLiveSupported() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// localExecutableDirs returns the absolute, de-duplicated search directories:
// the app's inherited PATH first, then conventional per-user and system install
// locations (a GUI app launched from Finder does not inherit a login PATH).
// Relative or parent-escaping entries are dropped; the list is bounded.
func localExecutableDirs(home string) []string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
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

// discoverLocalLive is the production discovery function for the local route.
// gopls is searched next to the selected toolchain first so a matching install
// wins over an unrelated one earlier on PATH. vtsls and node use the plain
// reviewed directories; node is taken from a global vtsls install's prefix
// when there is one.
func discoverLocalLive(root cd.AuthorizedRoot) ([]cd.Registration, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, diagnosticError("unsupported", "Home directory unavailable")
	}
	cache, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(cache) {
		return nil, diagnosticError("unsupported", "Cache directory unavailable")
	}
	dirs := localExecutableDirs(home)
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
		Go: cd.GoplsEnvironment{Home: home, Cache: filepath.Join(cache, "go-build"), ModuleCache: filepath.Join(home, "go", "pkg", "mod"), GoRoot: goroot},
		TS: cd.TSEnvironment{Home: home},
	})
}
