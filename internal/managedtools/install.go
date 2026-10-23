// Package managedtools installs a closed catalog of language servers only
// after explicit consent: gopls, Ruff, vtsls and Pyright (with Node.js for npm servers).
// Engine owns its lifetime; the desktop reaches it directly for the local host
// and through the managedTool RPC method on an SSH or WSL host, where the
// installer runs on that host. Installation never grants permission to execute
// a language server against a project.
package managedtools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/filelock"
)

// Binding must come from the backend owner, never from request JSON. Only
// host-computed diagnostics identities are accepted (local, ssh or wsl).
type Binding struct{ HostID, ConnectionID string }

type Proposal struct {
	Token        string `json:"token"`
	HostID       string `json:"hostId"`
	ConnectionID string `json:"connectionId"`
	Tool         string `json:"tool"`
	Version      string `json:"version"`
	Source       string `json:"source"`
	Integrity    string `json:"integrity"`
	Destination  string `json:"destination"`
	// Note names what else the install uses or downloads (the host Go for
	// gopls, Node.js for npm servers).
	Note    string    `json:"note,omitempty"`
	Expires time.Time `json:"expires"`
}

// spec is one resolved catalog entry: where it goes, the file its receipt
// binds, and how it is produced and validated.
type spec struct {
	name, version, policy, main string
	step                        string // what produce does, for failure messages
	produce                     func(ctx context.Context, i *Installer, stage, work string) error
	validate                    func(dir string) error
	needsNode                   bool
}

// Installer is connection-owned. Construct with the app data directory, not a
// caller-supplied destination. The embedding owner must cancel Install's
// context on disconnect/shutdown.
type Installer struct {
	mu      sync.Mutex
	binding Binding
	root    string
	goRoot  func() string
	// hostNode returns the prefix and version of a Node.js on the host that
	// npm servers can run on, or "" (UseHostNode).
	hostNode func() (prefix, version string)
	// nodeDir is the Node.js the running npm install uses; set while busy.
	nodeDir string
	pending *Proposal
	plan    spec
	busy    bool
	now     func() time.Time
	// Seams for tests; production values download over HTTPS and run the
	// host Go or the managed Node.js with an allowlisted environment.
	fetch   func(context.Context, string, io.Writer) error
	command func(context.Context, string, string, []string, ...string) ([]byte, error)
	build   func(context.Context, string, TrustedGo, goplsRelease) error
	gopls   func(string, goplsRelease) error
}

// New takes the app data directory and a function returning the host Go
// root ("" when none), which only a gopls proposal reads.
func New(data string, binding Binding, goRoot func() string) (*Installer, error) {
	if !hostBinding(binding) || !filepath.IsAbs(data) || filepath.Clean(data) != data || goRoot == nil {
		return nil, errors.New("managed tools: host identity and app data required")
	}
	// Unix private-directory semantics only. Windows ACL evidence is required
	// before admitting Windows; no best-effort chmod claim there.
	if _, ok := ruffArchives[platform()]; !ok || (runtime.GOOS != "darwin" && runtime.GOOS != "linux") {
		return nil, errors.New("managed tools: unsupported platform")
	}
	return &Installer{binding: binding, root: filepath.Join(data, "managed-tools"), goRoot: goRoot, now: time.Now,
		fetch: fetchHTTPS, command: runGo, build: buildGo, gopls: validateGopls}, nil
}

// UseHostNode sets how the host's Node.js is found. Without it, npm servers always
// uses the app's own Node.js.
func (i *Installer) UseHostNode(find func() (prefix, version string)) { i.hostNode = find }

// usableHostNode is the host Node.js when it also has its bundled npm.
func (i *Installer) usableHostNode() (prefix, version string) {
	if i.hostNode == nil {
		return "", ""
	}
	prefix, version = i.hostNode()
	major, err := strconv.Atoi(strings.Split(strings.TrimPrefix(version, "v"), ".")[0])
	if prefix == "" || err != nil || major < 18 || !filepath.IsAbs(prefix) || validateNode(prefix) != nil {
		return "", ""
	}
	return prefix, version
}

func hostBinding(b Binding) bool {
	kind, id, ok := strings.Cut(b.HostID, ":")
	return ok && (kind == "local" || kind == "ssh" || kind == "wsl") && digest(id) &&
		len(b.ConnectionID) > 0 && len(b.ConnectionID) <= 256 && !strings.ContainsAny(b.ConnectionID, "\x00\r\n")
}

func digest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}

// Proposable reports whether a client may name tool in a proposal.
func Proposable(tool string) bool { return slices.Contains(proposable, tool) }

func nodeSpec() spec {
	return spec{name: "node", version: nodeVersion, policy: nodePolicy, main: "bin/node", validate: validateNode, step: "downloading Node.js",
		produce: func(ctx context.Context, i *Installer, stage, work string) error {
			return i.installArchive(ctx, nodeArchives[platform()], work, stage)
		}}
}

func ruffSpec() spec {
	return spec{name: "ruff", version: ruffVersion, policy: ruffPolicy, main: "ruff", step: "downloading Ruff",
		validate: func(dir string) error { return executableFile(filepath.Join(dir, "ruff")) },
		produce: func(ctx context.Context, i *Installer, stage, work string) error {
			return i.installArchive(ctx, ruffArchives[platform()], work, stage)
		}}
}

func vtslsSpec() spec { return npmSpec(vtslsPackage()) }

func pyrightSpec() spec { return npmSpec(pyrightPackage()) }

func npmSpec(pkg npmPackage) spec {
	return spec{name: pkg.name, version: pkg.version, policy: npmPolicy, main: pkg.main, validate: pkg.validate, needsNode: true, step: "installing the " + pkg.name + " packages with npm",
		produce: func(ctx context.Context, i *Installer, stage, _ string) error {
			return installNPM(ctx, pkg, stage, i.nodeDir, i.command)
		}}
}

func goplsSpec(i *Installer, tool TrustedGo, r goplsRelease) spec {
	return spec{name: "gopls", version: r.Version, policy: goplsPolicy, main: "gopls", step: "building gopls",
		validate: func(dir string) error { return i.gopls(filepath.Join(dir, "gopls"), r) },
		produce: func(ctx context.Context, i *Installer, stage, _ string) error {
			return i.build(ctx, stage, tool, r)
		}}
}

// Propose replaces the previous single-use proposal. It never creates
// directories, executes a program or contacts the network; for gopls it reads
// the host Go's build metadata to pick the release that Go can build.
func (i *Installer) Propose(tool string) (Proposal, error) {
	if !Proposable(tool) {
		return Proposal{}, errors.New("managed tools: unknown tool")
	}
	var (
		s        spec
		source   string
		integ    string
		note     string
		platform = platform()
	)
	switch tool {
	case "gopls":
		g, goVersion, err := hostGo(i.goRoot())
		if err != nil {
			return Proposal{}, err
		}
		r, ok := goplsFor(goVersion)
		if !ok {
			return Proposal{}, ErrPrerequisite
		}
		s = goplsSpec(i, g, r)
		source = goProxy + " (" + goplsModule + "@" + r.Version + ")"
		integ = "Go checksum database; module " + r.Sum
		note = "Built with " + goVersion + " from " + filepath.Dir(filepath.Dir(g.Path)) + "."
	case "ruff":
		s = ruffSpec()
		a := ruffArchives[platform]
		source, integ = a.URL, "sha256:"+a.SHA256
	case "vtsls", "pyright":
		pkg := vtslsPackage()
		if tool == "pyright" {
			pkg = pyrightPackage()
		}
		s = npmSpec(pkg)
		source = fmt.Sprintf("%s (%s)", npmRegistry, pkg.source)
		integ = fmt.Sprintf("npm lockfile sha256:%x; every package checked against its sha512; install scripts off", sha256.Sum256(pkg.lockfile))
		if prefix, version := i.usableHostNode(); prefix != "" {
			note = "Uses Node.js " + version + " from " + prefix + "; Node.js is not downloaded."
		} else if _, err := i.discover(nodeSpec()); err != nil {
			a := nodeArchives[platform]
			note = "Also downloads Node.js " + nodeVersion + " to run it: " + a.URL + " (sha256:" + a.SHA256 + ")."
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.busy {
		return Proposal{}, errors.New("managed tools: busy")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Proposal{}, errors.New("managed tools: consent unavailable")
	}
	p := Proposal{
		Token: hex.EncodeToString(nonce[:]), HostID: i.binding.HostID, ConnectionID: i.binding.ConnectionID,
		Tool: tool, Version: s.version, Source: source, Integrity: integ, Note: note,
		Destination: filepath.Join(i.root, installDir(s.name, s.version)), Expires: i.now().Add(2 * time.Minute),
	}
	i.pending, i.plan = &p, s
	return p, nil
}

func (i *Installer) Dismiss() { i.mu.Lock(); i.pending = nil; i.mu.Unlock() }

// Install accepts only an opaque token plus the current backend-owned binding.
// It consumes consent even on failure; retries require a new user approval.
// No raw process output or filesystem errors escape this boundary.
func (i *Installer) Install(ctx context.Context, current Binding, token string) (path string, err error) {
	i.mu.Lock()
	p, s := i.pending, i.plan
	if i.busy || p == nil || p.Token != token || current != i.binding || !i.now().Before(p.Expires) {
		i.mu.Unlock()
		return "", errors.New("managed tools: stale or unavailable consent")
	}
	i.pending = nil
	i.busy = true
	i.mu.Unlock()
	defer func() { i.mu.Lock(); i.busy = false; i.mu.Unlock() }()
	requestCtx := ctx
	var failure *InstallFailure
	defer func() {
		if err != nil {
			path = ""
			switch {
			case requestCtx.Err() != nil:
				err = errors.New("managed tools: canceled")
			case errors.Is(err, ErrPrerequisite):
				err = ErrPrerequisite
			case errors.As(err, &failure):
				err = &InstallFailure{Step: failure.Step}
			default:
				err = errors.New("managed tools: installation failed; request fresh approval to retry")
			}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Refuse symlink components and non-private existing managed directories. The
	// parent app-data directory is backend-owned and must not be project-controlled.
	// A fresh SSH host may not have its parent (~/.local/share) yet.
	if base := filepath.Dir(filepath.Dir(i.root)); !exists(base) {
		if err = os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
	}
	if err = appDataTree(filepath.Dir(i.root)); err != nil {
		return "", err
	}
	if err = privateTree(i.root); err != nil {
		return "", err
	}
	lockPath := filepath.Join(i.root, "install.lock")
	if err = regularOrAbsent(lockPath); err != nil {
		return "", err
	}
	lock, e := filelock.TryLock(lockPath)
	if e != nil {
		return "", e
	}
	defer func() { _ = lock.Unlock() }()
	if s.needsNode {
		// Like Zed, use the host's Node.js when it can run npm servers; else the app's own.
		i.nodeDir, _ = i.usableHostNode()
		if i.nodeDir == "" {
			if _, err = i.place(ctx, nodeSpec()); err != nil {
				return "", err
			}
			i.nodeDir = filepath.Join(i.root, installDir("node", nodeVersion))
		}
	}
	return i.place(ctx, s)
}

// place installs s unless a valid install exists, and returns its main file.
func (i *Installer) place(ctx context.Context, s spec) (string, error) {
	dest := filepath.Join(i.root, installDir(s.name, s.version))
	if _, e := os.Lstat(dest); e == nil {
		if err := privateTree(dest); err != nil {
			return "", err
		}
		return i.existing(s, dest)
	} else if !os.IsNotExist(e) {
		return "", e
	}
	stage, err := os.MkdirTemp(i.root, ".staging-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	work, err := os.MkdirTemp(i.root, ".work-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err = s.produce(ctx, i, stage, work); err != nil {
		return "", &InstallFailure{Step: s.step, err: err}
	}
	if err = s.validate(stage); err != nil {
		return "", &InstallFailure{Step: "checking the installed " + s.name, err: err}
	}
	hash, err := fileDigest(filepath.Join(stage, filepath.FromSlash(s.main)))
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(receipt{s.version, s.policy, hash})
	if err = os.WriteFile(filepath.Join(stage, "receipt.json"), data, 0o600); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(stage, dest); err != nil {
		return "", err
	}
	return filepath.Join(dest, filepath.FromSlash(s.main)), nil
}

type receipt struct{ Version, Policy, SHA256 string }

// InstallFailure names the step an installation failed at. Install strips the
// cause, so it carries no process output or file system detail and may be shown.
type InstallFailure struct {
	Step string
	err  error
}

func (f *InstallFailure) Error() string {
	return "managed tools: installation failed while " + f.Step + "; request fresh approval to retry"
}

func (f *InstallFailure) Unwrap() error { return f.err }

// existing validates an install AND its receipt; a receipt alone is never
// authority. It returns the main file.
func (i *Installer) existing(s spec, dir string) (string, error) {
	if err := s.validate(dir); err != nil {
		return "", err
	}
	data, err := readBounded(filepath.Join(dir, "receipt.json"), 4096)
	if err != nil {
		return "", err
	}
	var r receipt
	if json.Unmarshal(data, &r) != nil || r.Version != s.version || r.Policy != s.policy {
		return "", errors.New("invalid receipt")
	}
	main := filepath.Join(dir, filepath.FromSlash(s.main))
	h, err := fileDigest(main)
	if err != nil || h != r.SHA256 {
		return "", errors.New("installation changed")
	}
	return main, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !os.IsNotExist(err)
}

func regularOrAbsent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("unsafe file")
	}
	return nil
}

func privateTree(path string) error {
	// Check ancestors before creating anything. Same-account malicious filesystem
	// mutation is outside this core's threat model; no project paths enter here.
	parent := filepath.Dir(path)
	if parent != path {
		if err := noLinks(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("unsafe private directory")
	}
	return nil
}

// appDataTree creates the app data directory private, or accepts an existing
// one that this account owns (see readAppData). Group or other write access,
// as the Linux desktop's web view storage leaves it, is removed; read access
// stays so that storage keeps working.
func appDataTree(path string) error {
	if err := noLinks(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedByUser(info) {
		return errors.New("unsafe app data directory")
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return os.Chmod(path, perm&^0o022)
	}
	return nil
}

func noLinks(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe ancestor")
	}
	parent := filepath.Dir(path)
	if parent != path {
		return noLinks(parent)
	}
	return nil
}

func fileDigest(path string) (string, error) {
	if err := regularOrAbsent(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return "", errors.New("invalid binary")
	}
	// Bounded allocation even if the file grows after stat.
	h := sha256.New()
	if _, err = copyBounded(h, f, 256<<20); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
