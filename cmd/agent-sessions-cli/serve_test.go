package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func TestServe_WithoutStdioFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	roots := paths.Roots{Cache: t.TempDir()}
	code := serveCmd(context.Background(), []string{}, roots, &stdout, &stderr, strings.NewReader(""))
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
		code := serveCmd(ctx, []string{"--stdio", "--nonce", nonce}, roots, sOutW, &stderr, sInR)
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

func TestServe_AdvisoryLockBusy(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "cache")
	roots := paths.Roots{
		Cache:        cacheDir,
		Config:       filepath.Join(tmpDir, "config"),
		Data:         filepath.Join(tmpDir, "data"),
		Claude:       filepath.Join(tmpDir, "claude"),
		Codex:        filepath.Join(tmpDir, "codex"),
		OpenCodeData: filepath.Join(tmpDir, "opencode"),
	}

	// Acquire lock first
	lock, err := rpc.AcquireCacheLock(cacheDir)
	if err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}
	defer lock.Unlock()

	sInR, sInW := io.Pipe()
	sOutR, sOutW := io.Pipe()
	nonce := rpc.GenerateNonce()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	serveDone := make(chan int, 1)
	go func() {
		code := serveCmd(ctx, []string{"--stdio", "--nonce", nonce}, roots, sOutW, &stderr, sInR)
		serveDone <- code
	}()

	rAfterPreface, err := rpc.WaitForPreface(ctx, sOutR, nonce)
	if err != nil {
		t.Fatalf("WaitForPreface failed: %v", err)
	}

	client := rpc.NewClient(rAfterPreface, sInW)
	defer client.Close()

	// Handshake should fail with ErrRemoteBusy
	_, err = client.Initialize(ctx, rpc.InitializeRequest{})
	if err == nil {
		t.Fatal("expected error from busy server, got nil")
	}
	if !errors.Is(err, rpc.ErrRemoteBusy) {
		t.Fatalf("expected ErrRemoteBusy, got: %v", err)
	}

	_ = sInW.Close()
	select {
	case code := <-serveDone:
		if code != 1 {
			t.Errorf("expected exit code 1 for busy server, got %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveCmd did not exit")
	}
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
