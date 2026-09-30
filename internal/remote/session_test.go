package remote

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func TestDialSession(t *testing.T) {
	bin := fakeSSHPath(t, "serve", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := DialSession(ctx, "box", SSHOptions{Binary: bin, ControlMaster: "no"}, "/opt/agent-sessions-cli", nil, nil)
	if err != nil {
		t.Fatalf("DialSession: %v", err)
	}
	defer s.Close()

	if s.Client() == nil || s.InitializeResult() == nil {
		t.Fatal("handshake did not complete")
	}
	if s.InitializeResult().ProtocolVersion != 1 {
		t.Fatalf("protocol = %d", s.InitializeResult().ProtocolVersion)
	}
	if !s.InitializeResult().Capabilities.Search {
		t.Fatal("expected search capability")
	}
	if !strings.Contains(s.Stderr(), "server ready") {
		t.Fatalf("stderr = %q", s.Stderr())
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not exit after Close")
	}
}

func TestDialSession_RejectsBadAlias(t *testing.T) {
	_, err := DialSession(context.Background(), "-oProxyCommand=x", SSHOptions{}, "/bin/true", nil, nil)
	if err == nil {
		t.Fatal("expected alias rejection")
	}
}

// The session heartbeats from before initialize, so a server with an idle
// timeout stays up for as long as the session does.
func TestHeartbeatKeepsIdleServerAlive(t *testing.T) {
	sInR, sInW := io.Pipe()
	defer sInW.Close()
	sOutR, sOutW := io.Pipe()
	srv := rpc.NewServer(nil, sInR, sOutW)
	srv.SetIdleTimeout(200 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()

	client := rpc.NewClient(sOutR, sInW)
	ctx, cancel := context.WithCancel(context.Background())
	go heartbeat(ctx, client, 40*time.Millisecond)

	select {
	case err := <-done:
		cancel()
		t.Fatalf("server exited while heartbeating: %v", err)
	case <-time.After(700 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, rpc.ErrIdleTimeout) {
			t.Fatalf("Serve = %v, want ErrIdleTimeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server kept running after heartbeats stopped")
	}
}
