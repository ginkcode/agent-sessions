package remote

import (
	"context"
	"strings"
	"testing"
	"time"
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
