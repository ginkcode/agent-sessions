package managedtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/filelock"
)

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
	b := Binding{"local:" + strings.Repeat("a", 64), "connection-1"}
	i, err := New(data, b, TrustedGo{filepath.Join(data, "go"), strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	i.build = func(_ context.Context, stage string, _ TrustedGo) error {
		return os.WriteFile(filepath.Join(stage, "gopls"), []byte("fake verified binary"), 0o700)
	}
	i.validate = func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(data) != "fake verified binary" {
			return errors.New("invalid binary")
		}
		return nil
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
	i.build = func(context.Context, string, TrustedGo) error { t.Fatal("unexpected build"); return nil }
	if _, err := i.Propose("https://attacker/tool"); err == nil {
		t.Fatal("unknown tool accepted")
	}
	p := propose(t, i)
	if p.Version != version || p.Source != source || p.Integrity != policy || p.HostID != i.binding.HostID || p.ConnectionID != i.binding.ConnectionID || !strings.HasPrefix(p.Destination, i.root+string(os.PathSeparator)) {
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
	i.build = func(c context.Context, s string, g TrustedGo) error { builds++; return build(c, s, g) }
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
				i.build = func(context.Context, string, TrustedGo) error { return errors.New("SECRET project/path raw output") }
			case "validate":
				i.validate = func(string) error { return errors.New("SECRET invalid metadata") }
			case "canceled":
				build := i.build
				i.build = func(c context.Context, s string, g TrustedGo) error { err := build(c, s, g); cancel(); return err }
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
	i.build = func(c context.Context, s string, g TrustedGo) error {
		calls.Add(1)
		close(started)
		<-release
		return build(c, s, g)
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

func TestUnsupportedBinding(t *testing.T) {
	data := t.TempDir()
	for _, host := range []string{"local:label", "ssh:" + strings.Repeat("a", 64), "wsl:" + strings.Repeat("a", 64), ""} {
		if _, err := New(data, Binding{host, "c"}, TrustedGo{filepath.Join(data, "go"), strings.Repeat("b", 64)}); err == nil {
			t.Fatal(host)
		}
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
	if err = validateBinary(path); err == nil {
		t.Fatal("fake binary accepted")
	}
}
