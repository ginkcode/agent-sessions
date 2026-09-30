package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestTryLockExcludesAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	a, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: err = %v, want ErrLocked", err)
	}
	if err := a.Unlock(); err != nil {
		t.Fatal(err)
	}
	_ = a.Unlock() // idempotent
	b, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	_ = b.Unlock()
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestAcquireWaitsForHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	a, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *Lock)
	go func() {
		l, err := Acquire(path, 10*time.Second)
		if err != nil {
			t.Error(err)
		}
		got <- l
	}()
	select {
	case <-got:
		t.Fatal("Acquire returned while the lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	_ = a.Unlock()
	select {
	case l := <-got:
		_ = l.Unlock()
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return after Unlock")
	}
}

// A holder that never lets go must not block Acquire forever.
func TestAcquireGivesUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	a, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Unlock() }()
	start := time.Now()
	if l, err := Acquire(path, 100*time.Millisecond); !errors.Is(err, ErrLocked) {
		_ = l.Unlock()
		t.Fatalf("Acquire on a held lock: err = %v, want ErrLocked", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Errorf("Acquire gave up after %v", d)
	}
}

func TestRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "x.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if l, err := TryLock(link); err == nil {
		_ = l.Unlock()
		t.Fatal("TryLock followed a symlink")
	}
}
