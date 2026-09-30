package remote

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// TestSSHIntegration runs the real probe, deploy and handshake against a
// host from ~/.ssh/config. It is opt-in: it writes the server into the
// host's ~/.cache/agent-sessions/server. Key or agent auth is required, since
// no askpass broker runs here. The version must be stamped to match the
// bundle (wails.json productVersion), for example:
//
//	make remote-servers
//	AGENT_SESSIONS_SSH_HOST=myhost go test ./internal/remote -run TestSSHIntegration -v -count=1 \
//	  -ldflags "-X github.com/ginkcode/agent-sessions/internal/version.Version=0.2.4"
//
// Set AGENT_SESSIONS_SSH_EXPECT_INSTALLED=1 on a second run to require that
// the installed build is reused rather than redeployed.
func TestSSHIntegration(t *testing.T) {
	host := os.Getenv("AGENT_SESSIONS_SSH_HOST")
	if host == "" {
		t.Skip("set AGENT_SESSIONS_SSH_HOST to run against a real ssh host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Default options, as the app uses: ControlMaster=auto on a private socket.
	opts := SSHOptions{}
	t.Cleanup(func() { _ = StopControlMaster(context.Background(), "", host, opts) })
	probe, err := ProbeHost(ctx, host, opts, version.Current())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("probe: %+v", *probe)
	if os.Getenv("AGENT_SESSIONS_SSH_EXPECT_INSTALLED") == "1" && len(probe.Installed) == 0 {
		t.Fatal("expected the server from a previous run to be reused")
	}

	sess, err := StartSession(ctx, host, opts, nil, engine.EmitterFunc(func(string, any) {}))
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer sess.Close()
	t.Logf("initialize: %+v", *sess.InitializeResult())

	counts, err := sess.Client().AgentCounts(ctx, engine.FilterOpts{})
	if err != nil {
		t.Fatalf("agent counts: %v (stderr: %s)", err, sess.Stderr())
	}
	t.Logf("agent counts: %v", counts)

	select {
	case <-sess.Done():
		t.Fatalf("session exited early (stderr: %s)", sess.Stderr())
	case <-time.After(3 * time.Second):
	}
}
