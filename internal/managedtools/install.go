// Package managedtools installs a closed catalog only after explicit consent.
// Engine owns its lifetime; RPC and desktop bindings are separate work. Installation never grants
// permission to execute a language server against a project.
package managedtools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/filelock"
)

const (
	module    = "golang.org/x/tools/gopls"
	version   = "v0.24.0"
	moduleSum = "h1:nPO/LffYa4sP62Ueuema5PPnZPGaZjGl+vZGvbTek4g="
	modSum    = "h1:abHXJMs6FnwH1z/R1GrYVo/TBfxyDbSFMCKakBQURg0="
	source    = "https://proxy.golang.org"
	policy    = "go-module-sumdb-v1"
)

// Binding must come from the backend owner, never from request JSON. Only stable
// local diagnostics identities are accepted; SSH/WSL fail closed pending CD-09.
type Binding struct{ HostID, ConnectionID string }

// TrustedGo is provisioned by trusted backend policy, NOT PATH/project discovery.
// SHA256 pins an independently trusted installed Go executable. Trust extends to
// its installation tree (compiler, standard library, tools); see design notes.
type TrustedGo struct{ Path, SHA256 string }

type Proposal struct {
	Token        string    `json:"token"`
	HostID       string    `json:"hostId"`
	ConnectionID string    `json:"connectionId"`
	Tool         string    `json:"tool"`
	Version      string    `json:"version"`
	Source       string    `json:"source"`
	Integrity    string    `json:"integrity"`
	Destination  string    `json:"destination"`
	Expires      time.Time `json:"expires"`
}

// Installer is connection-owned. Construct with the app data directory, not a
// caller-supplied destination. Constructor/Propose do no filesystem IO or probes.
// The embedding owner must cancel Install's context on disconnect/shutdown.
type Installer struct {
	mu       sync.Mutex
	binding  Binding
	root     string
	goTool   TrustedGo
	pending  *Proposal
	busy     bool
	now      func() time.Time
	build    func(context.Context, string, TrustedGo) error
	validate func(string) error
}

func New(data string, binding Binding, tool TrustedGo) (*Installer, error) {
	if !localBinding(binding) || !filepath.IsAbs(data) || filepath.Clean(data) != data ||
		!filepath.IsAbs(tool.Path) || filepath.Clean(tool.Path) != tool.Path || !digest(tool.SHA256) {
		return nil, errors.New("managed tools: trusted local host, app data and Go installation required")
	}
	// Unix private-directory semantics only in this unit. Windows ACL evidence is
	// required before admitting Windows; no best-effort chmod claim there.
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return nil, errors.New("managed tools: unsupported platform")
	}
	return &Installer{binding: binding, root: filepath.Join(data, "managed-tools"), goTool: tool, now: time.Now, build: buildGo, validate: validateBinary}, nil
}

func localBinding(b Binding) bool {
	return strings.HasPrefix(b.HostID, "local:") && digest(strings.TrimPrefix(b.HostID, "local:")) &&
		len(b.ConnectionID) > 0 && len(b.ConnectionID) <= 256 && !strings.ContainsAny(b.ConnectionID, "\x00\r\n")
}

func digest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}

// Propose replaces the previous single-use proposal without inspecting tools,
// creating directories, executing Go, or contacting the network. Not now may
// discard it; expiration also bounds abandoned proposals without a timer.
func (i *Installer) Propose(tool string) (Proposal, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if tool != "gopls" {
		return Proposal{}, errors.New("managed tools: unknown tool")
	}
	if i.busy {
		return Proposal{}, errors.New("managed tools: busy")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Proposal{}, errors.New("managed tools: consent unavailable")
	}
	p := Proposal{
		hex.EncodeToString(nonce[:]), i.binding.HostID, i.binding.ConnectionID, tool, version, source, policy,
		filepath.Join(i.root, "gopls-"+version+"-"+runtime.GOOS+"-"+runtime.GOARCH), i.now().Add(2 * time.Minute),
	}
	i.pending = &p
	return p, nil
}

func (i *Installer) Dismiss() { i.mu.Lock(); i.pending = nil; i.mu.Unlock() }

// Install accepts only an opaque token plus the current backend-owned binding.
// It consumes consent even on failure; retries require a new user approval.
// No raw process output or filesystem errors escape this boundary.
func (i *Installer) Install(ctx context.Context, current Binding, token string) (path string, err error) {
	i.mu.Lock()
	p := i.pending
	if i.busy || p == nil || p.Token != token || current != i.binding || !i.now().Before(p.Expires) {
		i.mu.Unlock()
		return "", errors.New("managed tools: stale or unavailable consent")
	}
	i.pending = nil
	i.busy = true
	i.mu.Unlock()
	defer func() { i.mu.Lock(); i.busy = false; i.mu.Unlock() }()
	requestCtx := ctx
	defer func() {
		if err != nil {
			path = ""
			switch {
			case requestCtx.Err() != nil:
				err = errors.New("managed tools: canceled")
			case errors.Is(err, ErrPrerequisite):
				err = ErrPrerequisite
			default:
				err = errors.New("managed tools: installation failed; request fresh approval to retry")
			}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Refuse symlink components and non-private existing managed directories. The
	// parent app-data directory is backend-owned and must not be project-controlled.
	if err = privateTree(filepath.Dir(i.root)); err != nil {
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
	if _, e = os.Lstat(p.Destination); e == nil {
		if err = privateTree(p.Destination); err != nil {
			return "", err
		}
		path = filepath.Join(p.Destination, "gopls")
		err = i.existing(path)
		return path, err
	} else if !os.IsNotExist(e) {
		return "", e
	}
	stage, e := os.MkdirTemp(i.root, ".staging-")
	if e != nil {
		return "", e
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err = i.build(ctx, stage, i.goTool); err != nil {
		return "", err
	}
	binary := filepath.Join(stage, "gopls")
	if err = i.validate(binary); err != nil {
		return "", err
	}
	hash, e := fileDigest(binary)
	if e != nil {
		return "", e
	}
	data, _ := json.Marshal(receipt{version, policy, hash})
	if err = os.WriteFile(filepath.Join(stage, "receipt.json"), data, 0o600); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(stage, p.Destination); err != nil {
		return "", err
	}
	return filepath.Join(p.Destination, "gopls"), nil
}

type receipt struct{ Version, Policy, SHA256 string }

func (i *Installer) existing(path string) error {
	if err := i.validate(path); err != nil {
		return err
	}
	receiptPath := filepath.Join(filepath.Dir(path), "receipt.json")
	data, err := readReceipt(receiptPath)
	if err != nil {
		return err
	}
	var r receipt
	if json.Unmarshal(data, &r) != nil || r.Version != version || r.Policy != policy {
		return errors.New("invalid receipt")
	}
	h, err := fileDigest(path)
	if err != nil || h != r.SHA256 {
		return errors.New("installation changed")
	}
	return nil
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
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return "", errors.New("invalid binary")
	}
	// Bounded allocation even if the file grows after stat.
	h := sha256.New()
	if _, err = copyBounded(h, f, 128<<20); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
