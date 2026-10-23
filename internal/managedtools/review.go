package managedtools

import (
	"debug/buildinfo"
	"errors"
	goversion "go/version"
	"os"
	"path/filepath"
	"runtime"
)

// TrustedGo pins the bytes of the host Go executable shown in a gopls
// proposal; the build refuses a toolchain that changed after consent. The Go
// installation is the user's own (found like an editor finds it: PATH, login
// shell PATH, conventional locations). Trust extends to its installation tree.
type TrustedGo struct{ Path, SHA256 string }

// ErrNoGo and ErrPrerequisite are safe, actionable text; raw process output
// never escapes.
var (
	ErrNoGo         = errors.New("managed tools: Go is not installed on this host; gopls is built with it. Install Go, then refresh")
	ErrPrerequisite = errors.New("managed tools: gopls needs Go 1.25 or newer on this host; automatic toolchain downloads are disabled")
)

// hostGo reads the toolchain's version from its build metadata; it never runs
// Go. The returned TrustedGo pins the executable's bytes for the build.
func hostGo(root string) (TrustedGo, string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return TrustedGo{}, "", ErrNoGo
	}
	path := filepath.Join(root, "bin", "go")
	hash, err := fileDigest(path)
	if err != nil {
		return TrustedGo{}, "", ErrNoGo
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm()&0o111 == 0 {
		return TrustedGo{}, "", ErrNoGo
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "cmd/go" || !goversion.IsValid(info.GoVersion) {
		return TrustedGo{}, "", ErrNoGo
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return TrustedGo{}, "", ErrNoGo
	}
	return TrustedGo{Path: path, SHA256: hash}, info.GoVersion, nil
}
