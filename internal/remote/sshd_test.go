package remote

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/remote/sshtest"
	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// These tests run against a Docker sshd (see package sshtest) and are
// skipped unless AGENT_SESSIONS_SSH_DOCKER=1. Run them with `make test-ssh`.

const fixtureSessionID = "11111111-2222-3333-4444-555555555555"

var fixtureRef = model.SessionRef{Agent: model.AgentClaude, ID: fixtureSessionID}

// startSSHD starts the container, seeds each user's ~/.claude with the
// project-x fixture, and points LocateServer at a freshly built bundle.
func startSSHD(t *testing.T) *sshtest.Host {
	t.Helper()
	h := sshtest.Start(t)
	t.Setenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR", sshtest.ServerBundleDir(t, version.Current()))
	fixture := filepath.Join("..", "provider", "claude", "testdata", "project-x")
	for _, user := range []string{sshtest.UserSh, sshtest.UserZsh} {
		h.CopyTree(t, fixture, user, ".claude/projects/-home-dev-project-x")
	}
	return h
}

func sshdOpts(h *sshtest.Host) SSHOptions {
	return SSHOptions{ConfigFile: h.ConfigFile, ControlPath: h.ControlPath}
}

func noopEmitter() engine.Emitter { return engine.EmitterFunc(func(string, any) {}) }

func TestSSHD_DeployReuseAndBrowse(t *testing.T) {
	h := startSSHD(t)
	opts := sshdOpts(h)

	for _, user := range []string{sshtest.UserSh, sshtest.UserZsh} {
		t.Run(user, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			alias := h.Alias(user)
			home := "/home/" + user

			// The login banner comes before the preface and must be skipped.
			probe, err := ProbeHost(ctx, alias, opts, ServerDirTag(version.Current(), ""))
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if probe.OS != "linux" || probe.Arch != runtime.GOARCH || probe.Home != home || probe.ServerPath != "" {
				t.Fatalf("fresh probe = %+v", *probe)
			}

			sess, err := StartSession(ctx, alias, opts, nil, noopEmitter())
			if err != nil {
				t.Fatalf("first start: %v", err)
			}
			init := sess.InitializeResult()
			if init.AppVersion != version.Current() || init.Roots.Claude != home+"/.claude" {
				t.Fatalf("initialize = %+v", *init)
			}
			browseFixture(ctx, t, sess)
			_ = sess.Close()

			// Installed privately, with no temp files left behind. The cache
			// root also holds the remote index, so it must be 0700 too.
			// Only permission bits count: Alpine homes are setgid, and new
			// directories inherit that.
			got := strings.Fields(h.ExecAs(t, user,
				`cd "$HOME/.cache/agent-sessions/server" && stat -c '%a' .. . */ */agent-sessions-cli && ls -A */`))
			for i, f := range got {
				if m, err := strconv.ParseUint(f, 8, 32); err == nil {
					got[i] = strconv.FormatUint(m&0o777, 8)
				}
			}
			if want := []string{"700", "700", "700", "700", "agent-sessions-cli"}; strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("install modes/listing = %v, want %v", got, want)
			}

			// A second connect reuses the build instead of redeploying.
			probe, err = ProbeHost(ctx, alias, opts, ServerDirTag(version.Current(), ""))
			if err != nil {
				t.Fatalf("second probe: %v", err)
			}
			if !strings.HasPrefix(probe.ServerPath, home+"/.cache/agent-sessions/server/"+version.Current()+"-") {
				t.Fatalf("second probe did not find the install: %+v", *probe)
			}
			before := h.ExecAs(t, user, `stat -c '%Y' "$HOME"/.cache/agent-sessions/server/*/agent-sessions-cli`)
			sess, err = StartSession(ctx, alias, opts, nil, noopEmitter())
			if err != nil {
				t.Fatalf("second start: %v", err)
			}
			browseFixture(ctx, t, sess)
			_ = sess.Close()
			if after := h.ExecAs(t, user, `stat -c '%Y' "$HOME"/.cache/agent-sessions/server/*/agent-sessions-cli`); after != before {
				t.Fatalf("server was redeployed: mtime %q -> %q", before, after)
			}
		})
	}
}

// browseFixture checks the remote catalog serves the seeded Claude session.
func browseFixture(ctx context.Context, t *testing.T, sess *Session) {
	t.Helper()
	c := sess.Client()
	// The app scans first too (appState.load).
	if err := c.Scan(ctx); err != nil {
		t.Fatalf("scan: %v%s", err, stderrNote(sess))
	}
	counts, err := c.AgentCounts(ctx, engine.FilterOpts{})
	if err != nil {
		t.Fatalf("agent counts: %v%s", err, stderrNote(sess))
	}
	if counts[string(model.AgentClaude)] < 1 {
		t.Fatalf("agent counts = %v, want the Claude fixture", counts)
	}
	meta, err := c.GetSessionMeta(ctx, fixtureRef)
	if err != nil {
		t.Fatalf("session meta: %v", err)
	}
	if meta.CWD != "/home/dev/project-x" {
		t.Fatalf("meta cwd = %q", meta.CWD)
	}
	page, err := c.GetMessages(ctx, fixtureRef, 0, 50)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(page.Messages) == 0 {
		t.Fatal("fixture transcript is empty")
	}
}

func TestSSHD_ServerExitEndsSession(t *testing.T) {
	h := startSSHD(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := StartSession(ctx, h.Alias(sshtest.UserSh), sshdOpts(h), nil, noopEmitter())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()

	h.Exec(t, `pkill -f 'agent-sessions-cli serve'`)
	select {
	case <-sess.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("session did not end after the remote server exited")
	}
	_, err = sess.Client().AgentCounts(ctx, engine.FilterOpts{})
	if !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("call after exit: err = %v, want ErrDisconnected", err)
	}
}

func TestSSHD_UnknownAliasFails(t *testing.T) {
	h := startSSHD(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Not in the temp config: resolution must not fall back to ~/.ssh/config.
	_, err := StartSession(ctx, "as-test-nobody", sshdOpts(h), nil, noopEmitter())
	if err == nil {
		t.Fatal("expected an unknown alias to fail")
	}
}

func stderrNote(sess *Session) string {
	if s := sess.Stderr(); s != "" {
		return " (stderr: " + s + ")"
	}
	return ""
}
