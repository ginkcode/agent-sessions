// Package codediagnostics implements host-side code diagnostics policy.
package codediagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Error uses the CD-01 bounded, plain-text error shape. Never include tool output.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string           { return e.Code + ": " + e.Message }
func failure(code, message string) error { return &Error{code, message} }
func bounded(s string, n int) bool {
	return s != "" && len(s) <= n && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// HostIdentity is supplied by the authenticated transport, never a UI label.
// Authority is the verified machine/account identity; Endpoint binds SSH routing
// or the WSL distribution installation. See code-diagnostics-trust.md.
type HostIdentity struct{ Kind, Authority, Endpoint string }

func (h HostIdentity) ID() (string, error) {
	if !bounded(h.Authority, 256) || !bounded(h.Endpoint, 256) {
		return "", failure("invalidArgument", "Verified host identity is required")
	}
	switch h.Kind {
	case "local", "ssh", "wsl":
	default:
		return "", failure("invalidArgument", "Unsupported host identity kind")
	}
	b, _ := json.Marshal(h)
	sum := sha256.Sum256(b)
	return h.Kind + ":" + hex.EncodeToString(sum[:]), nil
}

type Project struct {
	HostID string `json:"hostId"`
	Root   string `json:"root"`
}

// AuthorizedRoot cannot be constructed outside this package. It records the
// exact catalog-authorized spelling separately from its canonical host root.
type AuthorizedRoot struct {
	project   Project
	requested string
	known     func(string) bool
	identity  os.FileInfo
}

func (r AuthorizedRoot) Project() Project { return r.project }

// AuthorizeRoot calls known BEFORE touching the filesystem. It never promotes a
// nested directory to its repository parent. A host ID is transport-owned.
func AuthorizeRoot(hostID, dir string, known func(string) bool) (AuthorizedRoot, error) {
	if !bounded(hostID, 256) || !bounded(dir, 4096) || !filepath.IsAbs(dir) || hasParent(dir) {
		return AuthorizedRoot{}, failure("invalidArgument", "Expected an absolute host-native project root")
	}
	if known == nil || !known(dir) {
		return AuthorizedRoot{}, failure("unauthorized", "Project directory is not in the catalog")
	}
	real, info, err := canonicalDirectory(dir)
	if err != nil {
		return AuthorizedRoot{}, err
	}
	return AuthorizedRoot{Project{hostID, real}, dir, known, info}, nil
}

func canonicalDirectory(dir string) (string, os.FileInfo, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", nil, failure("notFound", "Project root cannot be resolved")
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", nil, failure("notFound", "Project root is not a directory")
	}
	return filepath.Clean(real), info, nil
}

func (r AuthorizedRoot) Revalidate() error {
	if r.known == nil || !r.known(r.requested) {
		return failure("unauthorized", "Project directory is no longer authorized")
	}
	real, info, err := canonicalDirectory(r.requested)
	if err != nil {
		return err
	}
	if real != r.project.Root || !os.SameFile(r.identity, info) {
		return failure("approvalChanged", "Project root identity changed")
	}
	return nil
}

func hasParent(s string) bool {
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || (runtime.GOOS == "windows" && r == '\\') }) {
		if p == ".." {
			return true
		}
	}
	return false
}

// ValidatePath validates the portable CD-01 diagnostic path, before host lookup.
func ValidatePath(s string) error {
	if !bounded(s, 4096) || strings.ContainsAny(s, "\\:") {
		return failure("invalidArgument", "Invalid diagnostic path")
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." {
			return failure("invalidArgument", "Invalid diagnostic path")
		}
	}
	// On Windows reject reserved names, alternate spellings and device paths too.
	if !filepath.IsLocal(filepath.FromSlash(s)) {
		return failure("invalidArgument", "Invalid diagnostic path")
	}
	if runtime.GOOS == "windows" {
		for _, p := range strings.Split(s, "/") {
			if strings.TrimRight(p, " .") != p {
				return failure("invalidArgument", "Ambiguous Windows diagnostic path")
			}
			if windowsDevice(p) {
				return failure("invalidArgument", "Invalid diagnostic path")
			}
		}
	}
	return nil
}

// windowsDevice reports a reserved device name, with or without an extension
// ("CON.txt" opens the console on Windows 10). Newer Go releases no longer
// treat the extension form as non-local, so it is checked here.
func windowsDevice(part string) bool {
	name, _, _ := strings.Cut(part, ".")
	name = strings.ToUpper(strings.TrimRight(name, " "))
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(name) < 4 || (name[:3] != "COM" && name[:3] != "LPT") {
		return false
	}
	switch name[3:] {
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
		return true
	}
	return false
}

// ResolvePath validates host ownership and containment using metadata only. No
// file body is opened; missing targets and symlink escapes fail closed.
func (r AuthorizedRoot) ResolvePath(hostID, rel string) (string, error) {
	if hostID != r.project.HostID {
		return "", failure("unauthorized", "Diagnostic belongs to another host")
	}
	if err := ValidatePath(rel); err != nil {
		return "", err
	}
	if err := r.Revalidate(); err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(r.project.Root, filepath.FromSlash(rel)))
	if err != nil {
		return "", failure("notFound", "Diagnostic target cannot be resolved")
	}
	contained, err := filepath.Rel(r.project.Root, target)
	if err != nil || contained == "." || !filepath.IsLocal(contained) {
		return "", failure("unauthorized", "Diagnostic target is outside the project")
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", failure("notFound", "Diagnostic target is not a regular file")
	}
	return target, nil
}

// ResolveURI accepts only authority-free file URIs on the selected host. Even
// localhost is rejected: remote identity is supplied separately, not by a URI.
func (r AuthorizedRoot) ResolveURI(hostID, raw string) (string, error) {
	native, err := nativeFilePath(raw)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(r.project.Root, native)
	if err != nil {
		return "", failure("unauthorized", "Diagnostic URI is outside the project")
	}
	rel = filepath.ToSlash(rel)
	if _, err := r.ResolvePath(hostID, rel); err != nil {
		return "", err
	}
	return rel, nil
}

// nativeFilePath is the absolute host-native path of an authority-free file
// URI, without any filesystem access.
func nativeFilePath(raw string) (string, error) {
	if !bounded(raw, 16384) {
		return "", failure("invalidArgument", "Invalid diagnostic URI")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", failure("invalidArgument", "Expected a host-local file URI")
	}
	path := u.Path
	if runtime.GOOS == "windows" && len(path) > 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	native := filepath.FromSlash(path)
	if !filepath.IsAbs(native) || hasParent(native) {
		return "", failure("invalidArgument", "Invalid diagnostic URI path")
	}
	return native, nil
}

// OutsideRootNotice is safe provider status, never a navigable raw tool path.
const OutsideRootNotice = "Some findings could not be linked because their files are outside the authorized project or could not be verified."
