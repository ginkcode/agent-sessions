package managedtools

import (
	"debug/buildinfo"
	"errors"
	goversion "go/version"
	"os"
	"path/filepath"
	"runtime"
)

// ReviewedGo is an opaque backend provisioning decision, not a request DTO.
// It pins the entrypoint after an operator has explicitly reviewed and trusted
// the COMPLETE installed Go tree. It cannot establish that trust by hashing it.
// No PATH discovery or environment-selected GOROOT is used.
type ReviewedGo struct{ tool TrustedGo }

// ReviewInstalledGo is for trusted backend composition only, never RPC/App input.
// root must be the operator-reviewed, canonical installed tree (not bin/go).
// This reads metadata only; it never probes, downloads, upgrades or executes Go.
// Re-provision after any tree update; Install rechecks the pinned entrypoint.
func ReviewInstalledGo(root string) (ReviewedGo, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || noLinks(root) != nil {
		return ReviewedGo{}, errors.New("managed tools: reviewed canonical Go installation required")
	}
	path := filepath.Join(root, "bin", "go")
	if noLinks(filepath.Dir(path)) != nil {
		return ReviewedGo{}, errors.New("managed tools: unsafe Go installation")
	}
	hash, err := fileDigest(path)
	if err != nil {
		return ReviewedGo{}, errors.New("managed tools: unsafe Go executable")
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm()&0o111 == 0 {
		return ReviewedGo{}, errors.New("managed tools: nonexecutable Go")
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || !goversion.IsValid(info.GoVersion) || goversion.Compare(info.GoVersion, "go1.27.0") < 0 {
		return ReviewedGo{}, ErrPrerequisite
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if info.Path != "cmd/go" || settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return ReviewedGo{}, ErrPrerequisite
	}
	return ReviewedGo{tool: TrustedGo{Path: path, SHA256: hash}}, nil
}

// MatchesRoot rechecks the pinned executable before metadata discovery uses a
// reviewed toolchain. Tree changes require explicit re-review, never auto repin.
func (r ReviewedGo) MatchesRoot(root string) bool {
	if filepath.Join(root, "bin", "go") != r.tool.Path || noLinks(root) != nil || noLinks(filepath.Join(root, "bin")) != nil {
		return false
	}
	hash, err := fileDigest(r.tool.Path)
	return err == nil && hash == r.tool.SHA256
}

// NewReviewed admits only an explicit backend review, never frontend path/hash.
func NewReviewed(data string, binding Binding, reviewed ReviewedGo) (*Installer, error) {
	return New(data, binding, reviewed.tool)
}

// ErrPrerequisite is safe, actionable text; raw process output never escapes.
var ErrPrerequisite = errors.New("managed tools: unsupported prerequisite; gopls v0.24.0 requires an explicitly reviewed installed Go 1.27.0 or newer; automatic toolchain upgrade is disabled")
