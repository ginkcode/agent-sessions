package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

// Two windows share the claims dir; each is its own process in the app, and
// here two hostClaims (two lock file handles) stand in for them.
func TestHostClaimOneWindowPerHost(t *testing.T) {
	dir := t.TempDir()
	var focused atomic.Int32
	first := newHostClaims(dir, func() { focused.Add(1) })
	second := newHostClaims(dir, nil)
	t.Cleanup(first.close)
	t.Cleanup(second.close)

	if err := first.claim("box"); err != nil {
		t.Fatal(err)
	}
	// Claiming again in the same window is fine (a retry or reconnect).
	if err := first.claim("box"); err != nil {
		t.Fatal(err)
	}
	err := second.claim("box")
	var inUse *hostInUseError
	if !errors.As(err, &inUse) || !errors.Is(err, ErrHostInOtherWindow) {
		t.Fatalf("second window claim = %v, want ErrHostInOtherWindow", err)
	}
	// Other hosts stay free.
	if err := second.claim("other"); err != nil {
		t.Fatal(err)
	}

	if err := second.focusOwner("box"); err != nil {
		t.Fatal(err)
	}
	if focused.Load() != 1 {
		t.Fatalf("focused %d times, want 1", focused.Load())
	}

	first.release("box")
	if err := second.claim("box"); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if err := first.claim("box"); !errors.Is(err, ErrHostInOtherWindow) {
		t.Fatalf("first window reclaim = %v, want ErrHostInOtherWindow", err)
	}
}

func TestHostClaimCloseReleasesAll(t *testing.T) {
	dir := t.TempDir()
	first := newHostClaims(dir, nil)
	second := newHostClaims(dir, nil)
	t.Cleanup(second.close)
	for _, h := range []string{"a", "wsl:Ubuntu"} {
		if err := first.claim(h); err != nil {
			t.Fatal(err)
		}
	}
	first.close()
	for _, h := range []string{"a", "wsl:Ubuntu"} {
		if err := second.claim(h); err != nil {
			t.Fatalf("claim %s after close: %v", h, err)
		}
	}
}

func TestHostClaimFocusRequiresToken(t *testing.T) {
	dir := t.TempDir()
	var focused atomic.Int32
	owner := newHostClaims(dir, func() { focused.Add(1) })
	t.Cleanup(owner.close)
	if err := owner.claim("box"); err != nil {
		t.Fatal(err)
	}
	info := owner.claimFile("box") + ".json"
	if fi, err := os.Stat(info); err != nil {
		t.Fatal(err)
	} else if platform.ModeBits && fi.Mode().Perm() != 0o600 {
		t.Fatalf("claim info mode %o, want 600", fi.Mode().Perm())
	}
	data, err := os.ReadFile(info)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(data), owner.token, strings.Repeat("0", len(owner.token)), 1)
	if err := os.WriteFile(info, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	other := newHostClaims(dir, nil)
	if err := other.focusOwner("box"); err == nil {
		t.Fatal("focus with a wrong token succeeded")
	}
	if focused.Load() != 0 {
		t.Fatal("window raised for a wrong token")
	}
}

// Choosing a host open in another window brings that window to the front
// and leaves this one as it was.
func TestConnectHostOpenInOtherWindow(t *testing.T) {
	dir := t.TempDir()
	var focused atomic.Int32
	owner := newConnection(nil)
	owner.dial = func(ctx context.Context, _ string, _ remote.SSHOptions, _ map[string]string, _ engine.Emitter) (*remote.Session, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	owner.stopMaster = nil
	owner.setClaims(newHostClaims(dir, func() { focused.Add(1) }))
	t.Cleanup(owner.Shutdown)

	other := newConnection(nil)
	other.dial = owner.dial
	other.stopMaster = nil
	other.setClaims(newHostClaims(dir, nil))
	t.Cleanup(other.Shutdown)

	if err := owner.Connect(context.Background(), "box"); err != nil {
		t.Fatal(err)
	}
	err := other.Connect(context.Background(), "box")
	if !errors.Is(err, ErrHostInOtherWindow) {
		t.Fatalf("Connect = %v, want ErrHostInOtherWindow", err)
	}
	if !strings.HasPrefix(err.Error(), "box is open in another window") || strings.Contains(err.Error(), "front") {
		t.Fatalf("error = %q", err)
	}
	if focused.Load() != 1 {
		t.Fatalf("owner focused %d times, want 1", focused.Load())
	}
	if st := other.snapshot(); st.Phase != ConnLocal || st.Host != "" {
		t.Fatalf("other window state = %+v, want Local", st)
	}

	// Switching to another host frees "box" for the other window.
	if err := owner.Connect(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if err := other.Connect(context.Background(), "box"); err != nil {
		t.Fatalf("Connect after the owner switched: %v", err)
	}
	if err := owner.Connect(context.Background(), "box"); !errors.Is(err, ErrHostInOtherWindow) {
		t.Fatalf("owner reconnect = %v", err)
	}
	// The owner keeps the host it had.
	if st := owner.snapshot(); st.Host != "next" {
		t.Fatalf("owner state = %+v, want still on next", st)
	}

	// Disconnecting frees the host.
	other.Disconnect()
	if err := owner.Connect(context.Background(), "box"); err != nil {
		t.Fatalf("Connect after Disconnect: %v", err)
	}
	// Shutting down (closing the window) frees it too.
	owner.Shutdown()
	deadline := time.Now().Add(time.Second)
	for {
		err := other.Connect(context.Background(), "box")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Connect after Shutdown: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClaimsDir(t *testing.T) {
	a := &App{}
	if a.claimsDir() != "" {
		t.Fatal("claims dir without a cache dir")
	}
	a.roots.Cache = "/cache"
	if got := a.claimsDir(); got != filepath.Join("/cache", "windows") {
		t.Fatal(got)
	}
	a.cacheDirOverride = "/override"
	if got := a.claimsDir(); got != filepath.Join("/override", "windows") {
		t.Fatal(got)
	}
}

func TestNewWindowStartsAnotherCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	out := filepath.Join(t.TempDir(), "env")
	orig := newWindowCommand
	t.Cleanup(func() { newWindowCommand = orig })
	newWindowCommand = func() (*exec.Cmd, error) {
		return exec.Command("/bin/sh", "-c", `env > "$0"`, out), nil
	}
	t.Setenv("AGENT_SESSIONS_ASKPASS_SOCK", "/tmp/sock")
	t.Setenv("AS_NEW_WINDOW_KEEP", "1")
	if err := (&App{}).NewWindow(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var env string
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(out)
		if env = string(data); strings.Contains(env, "AS_NEW_WINDOW_KEEP=1") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(env, "AS_NEW_WINDOW_KEEP=1") {
		t.Fatalf("new window not started; env %q", env)
	}
	if strings.Contains(env, "AGENT_SESSIONS_ASKPASS_") {
		t.Fatal("new window inherited the askpass session")
	}
}
