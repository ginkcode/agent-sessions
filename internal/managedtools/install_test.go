//go:build darwin || linux

package managedtools

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/filelock"
)

// hostGoRoot is a private copy of the Go that runs the tests: Propose reads
// its build metadata, never runs it, and the fake build never touches it.
var hostGoRoot = sync.OnceValues(func() (string, error) {
	//nolint:staticcheck // Tests read this host's toolchain metadata; no probe.
	src := filepath.Join(runtime.GOROOT(), "bin", "go")
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "managedtools-go-")
	if err != nil {
		return "", err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return "", err
	}
	if err = os.Mkdir(filepath.Join(dir, "bin"), 0o700); err != nil {
		return "", err
	}
	return dir, os.WriteFile(filepath.Join(dir, "bin", "go"), data, 0o700)
})

func TestMain(m *testing.M) {
	code := m.Run()
	if dir, err := hostGoRoot(); err == nil {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}

func fixture(t *testing.T) (*Installer, string) {
	t.Helper()
	// macOS temp roots may contain /var symlinks; the core requires canonical
	// backend-owned data directories, not display aliases.
	data, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(data, 0o700); err != nil {
		t.Fatal(err)
	}
	goRoot, err := hostGoRoot()
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{"local:" + strings.Repeat("a", 64), "connection-1"}
	i, err := New(data, b, func() string { return goRoot })
	if err != nil {
		t.Fatal(err)
	}
	i.build = func(_ context.Context, stage string, _ TrustedGo, _ goplsRelease) error {
		return os.WriteFile(filepath.Join(stage, "gopls"), []byte("fake verified binary"), 0o700)
	}
	i.gopls = func(path string, _ goplsRelease) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(data) != "fake verified binary" {
			return errors.New("invalid binary")
		}
		return nil
	}
	i.fetch = func(context.Context, string, io.Writer) error { t.Fatal("unexpected download"); return nil }
	i.command = func(context.Context, string, string, []string, ...string) ([]byte, error) {
		t.Fatal("unexpected command")
		return nil, nil
	}
	return i, data
}

func propose(t *testing.T, i *Installer) Proposal {
	t.Helper()
	p, err := i.Propose("gopls")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoConsentNoIO(t *testing.T) {
	i, data := fixture(t)
	i.build = func(context.Context, string, TrustedGo, goplsRelease) error { t.Fatal("unexpected build"); return nil }
	for _, tool := range []string{"https://attacker/tool", "node", "go", ""} {
		if _, err := i.Propose(tool); err == nil {
			t.Fatal("unknown tool accepted", tool)
		}
	}
	_, goVersion, err := hostGo(i.goRoot())
	if err != nil {
		t.Fatal(err)
	}
	r, ok := goplsFor(goVersion)
	if !ok {
		t.Skip("the Go running the tests cannot build any catalog gopls")
	}
	p := propose(t, i)
	if p.Tool != "gopls" || p.Version != r.Version || !strings.Contains(p.Source, goplsModule+"@"+r.Version) || !strings.Contains(p.Integrity, r.Sum) ||
		!strings.Contains(p.Note, "Built with "+goVersion) || p.HostID != i.binding.HostID || p.ConnectionID != i.binding.ConnectionID ||
		p.Destination != filepath.Join(i.root, installDir("gopls", r.Version)) {
		t.Fatalf("proposal: %+v", p)
	}
	if _, err := i.Install(context.Background(), i.binding, "wrong"); err == nil {
		t.Fatal("bad token")
	}
	for _, b := range []Binding{{i.binding.HostID, "reconnected"}, {"ssh:" + strings.Repeat("a", 64), i.binding.ConnectionID}} {
		if _, err := i.Install(context.Background(), b, p.Token); err == nil {
			t.Fatal("wrong binding")
		}
	}
	i.Dismiss()
	if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("dismissed token")
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preconsent IO: %v %v", entries, err)
	}
}

// Without a usable host Go, a gopls proposal is never made.
func TestGoplsProposalNeedsHostGo(t *testing.T) {
	i, _ := fixture(t)
	for _, root := range []string{"", "relative", t.TempDir()} {
		i.goRoot = func() string { return root }
		if _, err := i.Propose("gopls"); !errors.Is(err, ErrNoGo) {
			t.Fatalf("%q: %v", root, err)
		}
	}
	// A non-Go executable named go is not a toolchain.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "go"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	i.goRoot = func() string { return root }
	if _, err := i.Propose("gopls"); !errors.Is(err, ErrNoGo) {
		t.Fatal(err)
	}
}

func TestGoplsReleaseForHostGo(t *testing.T) {
	for goVersion, want := range map[string]string{
		"go1.28.1": "v0.24.0", "go1.27.0": "v0.24.0", "go1.26.8": "v0.23.0", "go1.26rc1": "v0.21.1", "go1.25.0": "v0.21.1", "go1.24.9": "", "devel": "",
	} {
		r, ok := goplsFor(goVersion)
		if ok != (want != "") || r.Version != want {
			t.Errorf("%s: %v %s", goVersion, ok, r.Version)
		}
	}
}

func TestConsentExpiryReplacementReplay(t *testing.T) {
	i, _ := fixture(t)
	now := time.Now()
	i.now = func() time.Time { return now }
	old := propose(t, i)
	p := propose(t, i)
	if _, err := i.Install(context.Background(), i.binding, old.Token); err == nil {
		t.Fatal("replaced consent")
	}
	now = now.Add(2 * time.Minute)
	if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("expired consent")
	}
	p = propose(t, i)
	path, err := i.Install(context.Background(), i.binding, p.Token)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(p.Destination, "gopls") {
		t.Fatal(path)
	}
	if _, err = i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("replay")
	}
}

func TestPublishReuseAndTamper(t *testing.T) {
	i, _ := fixture(t)
	var builds int
	build := i.build
	i.build = func(c context.Context, s string, g TrustedGo, r goplsRelease) error {
		builds++
		return build(c, s, g, r)
	}
	p := propose(t, i)
	path, err := i.Install(context.Background(), i.binding, p.Token)
	if err != nil {
		t.Fatal(err)
	}
	p = propose(t, i)
	if _, err = i.Install(context.Background(), i.binding, p.Token); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatal(builds)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), "receipt.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p = propose(t, i)
	if _, err = i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("tampered receipt reused")
	}
	if builds != 1 {
		t.Fatal("silently rebuilt")
	}
}

func TestFailureCleanupAndRedaction(t *testing.T) {
	for _, kind := range []string{"build", "validate", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			i, _ := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "build":
				i.build = func(context.Context, string, TrustedGo, goplsRelease) error {
					return errors.New("SECRET project/path raw output")
				}
			case "validate":
				i.gopls = func(string, goplsRelease) error { return errors.New("SECRET invalid metadata") }
			case "canceled":
				build := i.build
				i.build = func(c context.Context, s string, g TrustedGo, r goplsRelease) error {
					err := build(c, s, g, r)
					cancel()
					return err
				}
			}
			p := propose(t, i)
			if _, err := i.Install(ctx, i.binding, p.Token); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("error: %v", err)
			}
			if _, err := os.Stat(p.Destination); !os.IsNotExist(err) {
				t.Fatal("partial published")
			}
			entries, err := os.ReadDir(i.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "install.lock" {
					t.Fatal("staging leaked", entry.Name())
				}
			}
			if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
				t.Fatal("failed consent reused")
			}
		})
	}
}

func TestConcurrentInstallAndProcessLock(t *testing.T) {
	i, _ := fixture(t)
	p := propose(t, i)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	build := i.build
	var calls atomic.Int32
	i.build = func(c context.Context, s string, g TrustedGo, r goplsRelease) error {
		calls.Add(1)
		close(started)
		<-release
		return build(c, s, g, r)
	}
	go func() { _, err := i.Install(context.Background(), i.binding, p.Token); done <- err }()
	<-started
	if _, err := i.Propose("gopls"); err == nil {
		t.Fatal("busy propose")
	}
	if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("concurrent install")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	lock, err := filelock.TryLock(filepath.Join(i.root, "install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	p = propose(t, i)
	if _, err = i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("cross-process lock ignored")
	}
}

// A fresh host may lack the app data directory and its parent.
func TestInstallCreatesMissingAppData(t *testing.T) {
	i, base := fixture(t)
	data := filepath.Join(base, "share", "agent-sessions")
	n, err := New(data, i.binding, i.goRoot)
	if err != nil {
		t.Fatal(err)
	}
	n.build, n.gopls = i.build, i.gopls
	if _, err := n.Discover("gopls"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	p := propose(t, n)
	if _, err := n.Install(context.Background(), n.binding, p.Token); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(data); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatal(st, err)
	}
}

// The Linux desktop's web view creates the app data directory group-writable.
// Nothing installed is still "missing" there (so setup is offered), and
// installing removes only the write access of others.
func TestInstallSharedAppData(t *testing.T) {
	i, data := fixture(t)
	if err := os.Chmod(data, 0o775); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("gopls"); !os.IsNotExist(err) {
		t.Fatalf("missing install reported as %v", err)
	}
	p := propose(t, i)
	binary, err := i.Install(context.Background(), i.binding, p.Token)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(data); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatal(st, err)
	}
	if got, err := i.Discover("gopls"); err != nil || got != binary {
		t.Fatalf("%s %v", got, err)
	}
	// An existing install below a data directory others can write is not used.
	if err := os.Chmod(data, 0o775); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("gopls"); err == nil || os.IsNotExist(err) {
		t.Fatalf("install under writable data accepted: %v", err)
	}
}

func TestUnsafeFilesystem(t *testing.T) {
	for _, kind := range []string{"root-link", "root-public", "lock-link", "destination-link", "binary-link", "receipt-link"} {
		t.Run(kind, func(t *testing.T) {
			i, data := fixture(t)
			outside := filepath.Join(data, "outside")
			if err := os.Mkdir(outside, 0o700); err != nil {
				t.Fatal(err)
			}
			p := propose(t, i)
			if kind == "root-link" {
				if err := os.Symlink(outside, i.root); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(i.root, 0o700); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "root-public":
					if err := os.Chmod(i.root, 0o755); err != nil {
						t.Fatal(err)
					}
				case "lock-link":
					if err := os.Symlink(filepath.Join(outside, "lock"), filepath.Join(i.root, "install.lock")); err != nil {
						t.Fatal(err)
					}
				case "destination-link":
					if err := os.Symlink(outside, p.Destination); err != nil {
						t.Fatal(err)
					}
				default:
					if err := os.Mkdir(p.Destination, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(p.Destination, "gopls"), []byte("fake verified binary"), 0o700); err != nil {
						t.Fatal(err)
					}
					leaf := "receipt.json"
					if kind == "binary-link" {
						leaf = "gopls"
						if err := os.Remove(filepath.Join(p.Destination, leaf)); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(p.Destination, leaf)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
				t.Fatal("unsafe path accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("outside changed", entries, err)
			}
		})
	}
}

// Host-computed identities of every kind are accepted; labels are not.
func TestBinding(t *testing.T) {
	data := t.TempDir()
	goRoot := func() string { return "" }
	for _, host := range []string{"local:label", "ftp:" + strings.Repeat("a", 64), "ssh:" + strings.Repeat("A", 64), "wsl:", ""} {
		if _, err := New(data, Binding{host, "c"}, goRoot); err == nil {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"local:", "ssh:", "wsl:"} {
		if _, err := New(data, Binding{host + strings.Repeat("a", 64), "c"}, goRoot); err != nil {
			t.Fatal(host, err)
		}
	}
	for _, c := range []string{"", "a\nb", strings.Repeat("c", 257)} {
		if _, err := New(data, Binding{"ssh:" + strings.Repeat("a", 64), c}, goRoot); err == nil {
			t.Fatalf("connection %q", c)
		}
	}
	if _, err := New("relative", Binding{"ssh:" + strings.Repeat("a", 64), "c"}, goRoot); err == nil {
		t.Fatal("relative data")
	}
}

func TestBoundedOutputAndBinaryValidation(t *testing.T) {
	var out cappedOutput
	huge := []byte(strings.Repeat("x", 128<<10))
	n, err := out.Write(huge)
	if err != nil || n != len(huge) || !out.exceeded || out.buf.Len() != 64<<10 {
		t.Fatal("unbounded output")
	}
	path := filepath.Join(t.TempDir(), "fake")
	if err = os.WriteFile(path, []byte("not a Go binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = validateGopls(path, goplsReleases[0]); err == nil {
		t.Fatal("fake binary accepted")
	}
}
