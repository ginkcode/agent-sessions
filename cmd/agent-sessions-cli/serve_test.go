package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// rootsKey names the index the server's engine uses for roots.
func rootsKey(r paths.Roots) string { return index.RootsKey(r.Claude, r.Codex, r.OpenCodeData) }

func fixedRoots(r paths.Roots) func() (paths.Roots, error) {
	return func() (paths.Roots, error) { return r, nil }
}

func TestServe_WithoutStdioFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	roots := paths.Roots{Cache: t.TempDir()}
	code := serveCmd(context.Background(), []string{}, fixedRoots(roots), &stdout, &stderr, strings.NewReader(""))
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--stdio flag is required") {
		t.Errorf("expected error message about --stdio, got: %s", stderr.String())
	}
}

func TestServe_PrefaceAndRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	roots := paths.Roots{
		Cache:        filepath.Join(tmpDir, "cache"),
		Config:       filepath.Join(tmpDir, "config"),
		Data:         filepath.Join(tmpDir, "data"),
		Claude:       filepath.Join(tmpDir, "claude"),
		Codex:        filepath.Join(tmpDir, "codex"),
		OpenCodeData: filepath.Join(tmpDir, "opencode"),
	}

	// Pipes for stdio
	// clientWriter -> serverStdin
	sInR, sInW := io.Pipe()
	// serverStdout -> clientReader
	sOutR, sOutW := io.Pipe()

	nonce := rpc.GenerateNonce()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	serveDone := make(chan int, 1)
	go func() {
		code := serveCmd(ctx, []string{"--stdio", "--nonce", nonce}, fixedRoots(roots), sOutW, &stderr, sInR)
		serveDone <- code
	}()

	// 1. Wait for preface from server stdout
	rAfterPreface, err := rpc.WaitForPreface(ctx, sOutR, nonce)
	if err != nil {
		t.Fatalf("WaitForPreface failed: %v", err)
	}

	// 2. Initialize client over the connection
	client := rpc.NewClient(rAfterPreface, sInW)
	defer client.Close()

	initRes, err := client.Initialize(ctx, rpc.InitializeRequest{
		ProtocolVersion: rpc.ProtocolVersion,
		AppVersion:      "v0.3.0",
	})
	if err != nil {
		t.Fatalf("client.Initialize failed: %v, stderr: %s", err, stderr.String())
	}
	if initRes.ProtocolVersion != rpc.ProtocolVersion {
		t.Errorf("got protocol version %d, want %d", initRes.ProtocolVersion, rpc.ProtocolVersion)
	}

	// 3. Ping
	pong, err := client.Ping(ctx, "remote-host")
	if err != nil {
		t.Fatalf("client.Ping failed: %v", err)
	}
	if !strings.Contains(pong, "remote-host") {
		t.Errorf("unexpected ping response: %q", pong)
	}

	// 4. Scan
	if err := client.Scan(ctx); err != nil {
		t.Fatalf("client.Scan failed: %v", err)
	}

	// 5. Close stdin to simulate EOF / client disconnect
	_ = sInW.Close()

	select {
	case code := <-serveDone:
		if code != 0 {
			t.Errorf("serveCmd exited with code %d, stderr: %s", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveCmd did not exit on stdin EOF")
	}
}

type serveHandle struct {
	client *rpc.Client
	stdin  io.Closer
	done   chan int
	stderr *bytes.Buffer
}

func startServe(t *testing.T, ctx context.Context, roots paths.Roots) *serveHandle {
	t.Helper()
	sInR, sInW := io.Pipe()
	sOutR, sOutW := io.Pipe()
	nonce := rpc.GenerateNonce()
	h := &serveHandle{stdin: sInW, done: make(chan int, 1), stderr: &bytes.Buffer{}}
	go func() {
		h.done <- serveCmd(ctx, []string{"--stdio", "--nonce", nonce}, fixedRoots(roots), sOutW, h.stderr, sInR)
	}()
	r, err := rpc.WaitForPreface(ctx, sOutR, nonce)
	if err != nil {
		t.Fatalf("WaitForPreface: %v", err)
	}
	h.client = rpc.NewClient(r, sInW)
	t.Cleanup(func() { _ = h.client.Close() })
	if _, err := h.client.Initialize(ctx, rpc.InitializeRequest{ProtocolVersion: rpc.ProtocolVersion}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return h
}

func (h *serveHandle) stop(t *testing.T) {
	t.Helper()
	_ = h.stdin.Close()
	select {
	case code := <-h.done:
		if code != 0 {
			t.Errorf("exit %d, stderr: %s", code, h.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveCmd did not exit")
	}
}

// Two clients on one host each get a server; neither blocks the other, and
// one of them is the index writer.
func TestServe_TwoClientsShareOneCache(t *testing.T) {
	tmpDir := t.TempDir()
	claude := filepath.Join(tmpDir, "claude")
	if err := os.MkdirAll(filepath.Join(claude, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	roots := paths.Roots{
		Cache:  filepath.Join(tmpDir, "cache"),
		Config: filepath.Join(tmpDir, "config"),
		Data:   filepath.Join(tmpDir, "data"),
		Claude: claude,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := startServe(t, ctx, roots)
	second := startServe(t, ctx, roots)
	for i, h := range []*serveHandle{first, second} {
		if _, err := h.client.Ping(ctx, "x"); err != nil {
			t.Fatalf("client %d ping: %v", i, err)
		}
		if _, err := h.client.Search(ctx, "anything", index.SearchFilter{}); err != nil {
			t.Errorf("client %d search: %v", i, err)
		}
	}
	if _, err := index.TryLock(roots.Cache, rootsKey(roots)); !errors.Is(err, index.ErrLocked) {
		t.Errorf("index lock while serving: err = %v, want ErrLocked", err)
	}

	first.stop(t)
	if _, err := second.client.Ping(ctx, "x"); err != nil {
		t.Fatalf("second client after the first left: %v", err)
	}
	second.stop(t)
	l, err := index.TryLock(roots.Cache, rootsKey(roots))
	if err != nil {
		t.Fatalf("index lock after both exited: %v", err)
	}
	_ = l.Unlock()
}

// The client's env overrides must reach the roots the engine, its index
// and the transfer staging dir use, so they apply before the engine starts.
func TestServe_ClientEnvAppliesBeforeEngineStarts(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Registered so cleanup restores what the server's os.Setenv overwrites.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, "default-claude"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "default-cache"))

	overClaude := filepath.Join(tmp, "over-claude")
	overCache := filepath.Join(tmp, "over-cache")

	sInR, sInW := io.Pipe()
	sOutR, sOutW := io.Pipe()
	nonce := rpc.GenerateNonce()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	serveDone := make(chan int, 1)
	go func() {
		serveDone <- serveCmd(ctx, []string{"--stdio", "--nonce", nonce}, paths.Default, sOutW, &stderr, sInR)
	}()

	r, err := rpc.WaitForPreface(ctx, sOutR, nonce)
	if err != nil {
		t.Fatal(err)
	}
	client := rpc.NewClient(r, sInW)
	defer client.Close()

	res, err := client.Initialize(ctx, rpc.InitializeRequest{
		ProtocolVersion: rpc.ProtocolVersion,
		ClientEnv: map[string]string{
			"CLAUDE_CONFIG_DIR": overClaude,
			"XDG_CACHE_HOME":    overCache,
			"PATH":              "/nowhere", // not allowlisted
		},
	})
	if err != nil {
		t.Fatalf("initialize: %v (stderr: %s)", err, stderr.String())
	}
	if os.Getenv("PATH") == "/nowhere" {
		t.Fatal("non-allowlisted PATH was applied")
	}
	if res.Roots.Claude != overClaude {
		t.Errorf("roots.Claude = %q, want %q", res.Roots.Claude, overClaude)
	}
	if runtime.GOOS == "linux" {
		wantCache := filepath.Join(overCache, "agent-sessions")
		if res.Roots.Cache != wantCache {
			t.Errorf("roots.Cache = %q, want %q", res.Roots.Cache, wantCache)
		}
		// The index lives in the overridden cache dir.
		if _, err := index.TryLock(wantCache, rootsKey(res.Roots)); !errors.Is(err, index.ErrLocked) {
			t.Errorf("index lock in overridden cache: err = %v, want ErrLocked", err)
		}
	}

	if _, err := client.Initialize(ctx, rpc.InitializeRequest{ProtocolVersion: rpc.ProtocolVersion}); err == nil {
		t.Error("second initialize succeeded")
	}
	if _, err := client.Ping(ctx, "x"); err != nil {
		t.Fatalf("ping after initialize: %v", err)
	}

	_ = sInW.Close()
	select {
	case code := <-serveDone:
		if code != 0 {
			t.Errorf("exit %d, stderr: %s", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveCmd did not exit")
	}
}

// A client that goes silent without closing the pipe, as behind a half-open
// ssh connection, must not keep the server and its index lock alive.
func TestServe_IdleTimeoutReleasesLock(t *testing.T) {
	tmpDir := t.TempDir()
	roots := paths.Roots{
		Cache:  filepath.Join(tmpDir, "cache"),
		Claude: filepath.Join(tmpDir, "claude"),
	}
	sInR, sInW := io.Pipe()
	defer sInW.Close()
	sOutR, sOutW := io.Pipe()
	nonce := rpc.GenerateNonce()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	serveDone := make(chan int, 1)
	go func() {
		serveDone <- serveCmd(ctx, []string{"--stdio", "--nonce", nonce, "--idle-timeout", "300ms"}, fixedRoots(roots), sOutW, &stderr, sInR)
	}()
	r, err := rpc.WaitForPreface(ctx, sOutR, nonce)
	if err != nil {
		t.Fatal(err)
	}
	client := rpc.NewClient(r, sInW)
	if _, err := client.Initialize(ctx, rpc.InitializeRequest{ProtocolVersion: rpc.ProtocolVersion}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := index.TryLock(roots.Cache, rootsKey(roots)); !errors.Is(err, index.ErrLocked) {
		t.Fatalf("lock while serving: err = %v, want ErrLocked", err)
	}

	// Stdin stays open; the client just stops sending.
	select {
	case code := <-serveDone:
		if code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveCmd did not exit on idle")
	}
	if !strings.Contains(stderr.String(), "no message from the client") {
		t.Errorf("stderr = %q", stderr.String())
	}
	lock, err := index.TryLock(roots.Cache, rootsKey(roots))
	if err != nil {
		t.Fatalf("lock after idle exit: %v", err)
	}
	_ = lock.Unlock()
}

func TestNoWailsDependency(t *testing.T) {
	// The remote server must not link the GUI (Wails, internal/app) or the
	// test-only ssh harness.
	assertNoDeps(t, []string{"."},
		"github.com/wailsapp/wails",
		"github.com/ginkcode/agent-sessions/internal/app",
		"github.com/ginkcode/agent-sessions/internal/remote/sshtest")
}

func TestDesktopDoesNotLinkTestHarness(t *testing.T) {
	assertNoDeps(t, []string{"-tags", "webkit2_41,production", "../agent-sessions"},
		"github.com/ginkcode/agent-sessions/internal/remote/sshtest")
}

func assertNoDeps(t *testing.T, args []string, forbidden ...string) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-deps"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %v failed: %v, output: %s", args, err, out)
	}
	for _, dep := range strings.Split(string(out), "\n") {
		for _, f := range forbidden {
			if dep == f || strings.HasPrefix(dep, f+"/") {
				t.Errorf("go list -deps %v: forbidden dependency %s", args, dep)
			}
		}
	}
}
