package remote

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
			probe, err := ProbeHost(ctx, alias, opts, version.Current())
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if probe.OS != "linux" || probe.Arch != runtime.GOARCH || probe.Home != home || len(probe.Installed) != 0 {
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
			probe, err = ProbeHost(ctx, alias, opts, version.Current())
			if err != nil {
				t.Fatalf("second probe: %v", err)
			}
			if len(probe.Installed) != 1 || !strings.HasPrefix(probe.Installed[0], version.Current()+"-") {
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

// A session that grows on the host shows its new messages after a rescan,
// as when the app's Refresh button is clicked on a live session.
func TestSSHD_RescanServesNewMessages(t *testing.T) {
	h := startSSHD(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := StartSession(ctx, h.Alias(sshtest.UserSh), sshdOpts(h), nil, noopEmitter())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()
	c := sess.Client()
	if err := c.Scan(ctx); err != nil {
		t.Fatalf("scan: %v%s", err, stderrNote(sess))
	}
	before, err := c.GetMessages(ctx, fixtureRef, 0, 50)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}

	h.ExecAs(t, sshtest.UserSh, `printf '%s\n' '{"type":"user","uuid":"parent-u3","sessionId":"`+fixtureSessionID+
		`","timestamp":"2026-09-20T16:02:00Z","cwd":"/home/dev/project-x","message":{"role":"user","content":"One more thing"}}' `+
		`>> "$HOME/.claude/projects/-home-dev-project-x/`+fixtureSessionID+`.jsonl"`)
	if err := c.Scan(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	after, err := c.GetMessages(ctx, fixtureRef, 0, 50)
	if err != nil {
		t.Fatalf("messages after rescan: %v", err)
	}
	if after.TotalCount != before.TotalCount+1 {
		t.Fatalf("messages after rescan = %d, want %d", after.TotalCount, before.TotalCount+1)
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

// The server must talk only over its stdio: no TCP/unix listeners and no UDP
// sockets, even after a scan has started the index and watcher.
func TestSSHD_ServeOpensNoListeningSockets(t *testing.T) {
	h := startSSHD(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := StartSession(ctx, h.Alias(sshtest.UserSh), sshdOpts(h), nil, noopEmitter())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()
	if err := sess.Client().Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// For every socket fd of the server process, look its inode up in the
	// process's view of /proc/net. tcp: st is $4 (0A = LISTEN), inode $10.
	// unix: flags $4 (00010000 = listening), inode $7. The same script
	// reports sshd's own :22 listener, so it does see sockets; the FDS line
	// proves each matched process's fd table was readable.
	out := h.Exec(t, `
pids=$(pgrep -f '[a]gent-sessions-cli.* serve --stdio') # [a]: not this script
[ -n "$pids" ] || { echo NO_SERVER; exit 0; }
for pid in $pids; do
  echo "PID $pid"
  echo "FDS $(ls /proc/$pid/fd | sort -n | tr '\n' ' ')"
  for fd in /proc/$pid/fd/*; do
    l=$(readlink "$fd" 2>/dev/null) || continue
    case "$l" in socket:\[*\]) ;; *) continue;; esac
    ino=${l#socket:[}; ino=${ino%]}
    awk -v i="$ino" '$10==i && $4=="0A" {print "TCP_LISTEN " $2}' /proc/$pid/net/tcp /proc/$pid/net/tcp6
    awk -v i="$ino" '$10==i {print "UDP " $2}' /proc/$pid/net/udp /proc/$pid/net/udp6
    awk -v i="$ino" '$7==i && $4=="00010000" {print "UNIX_LISTEN " $8}' /proc/$pid/net/unix
  done
done`)
	if strings.Contains(out, "NO_SERVER") || !strings.Contains(out, "PID ") {
		t.Fatalf("server process not found: %q", out)
	}
	t.Logf("server sockets probe:\n%s", out)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case strings.HasPrefix(line, "PID "):
		case strings.HasPrefix(line, "FDS "):
			if !strings.HasPrefix(line, "FDS 0 1 2 ") {
				t.Errorf("server fd table not readable: %q", line)
			}
		default:
			t.Errorf("server has a socket it should not: %s", line)
		}
	}
}

// countServe reports how many remote servers are running, for all users.
func countServe(t *testing.T, h *sshtest.Host) int {
	t.Helper()
	out := strings.TrimSpace(h.Exec(t, `pgrep -f '[a]gent-sessions-cli.* serve --stdio' | wc -l`))
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("pgrep count %q: %v", out, err)
	}
	return n
}

func waitServeCount(t *testing.T, h *sshtest.Host, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := countServe(t, h)
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d remote servers running, want %d", n, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Closing a session ends its server. A client that goes silent while its
// connection stays open, as behind a half-open link sshd has not noticed,
// is ended by the server's idle timeout.
func TestSSHD_NoServerLeftBehind(t *testing.T) {
	h := startSSHD(t)
	opts := sshdOpts(h)
	alias := h.Alias(sshtest.UserSh)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := StartSession(ctx, alias, opts, nil, noopEmitter())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	bin := sess.bin
	waitServeCount(t, h, 1, 5*time.Second)
	_ = sess.Close()
	waitServeCount(t, h, 0, 10*time.Second)

	// Run the server by hand with a short idle timeout and never write to
	// it, keeping stdin open.
	script := QuotePOSIX(bin) + " serve --stdio --idle-timeout 2s"
	opts.NoTTY = true
	cmd, err := BuildSSHCmd(ctx, alias, []string{LoginShell, "-lc", script}, opts)
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitServeCount(t, h, 1, 5*time.Second)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("silent client: remote server did not exit on its idle timeout")
	}
	if !strings.Contains(stderr.String(), "no message from the client") {
		t.Errorf("stderr = %q", stderr.String())
	}
	waitServeCount(t, h, 0, 5*time.Second)
}

// serveRoles reports "writer" or "reader" for every remote server, sorted.
// /proc/<pid>/fd symlink targets are hidden across Docker exec sessions, so
// match the writer's FLOCK owner in /proc/locks to the server PIDs instead.
func serveRoles(t *testing.T, h *sshtest.Host) []string {
	t.Helper()
	out := h.Exec(t, `
writers=$(awk '$2=="FLOCK" && $4=="WRITE" {print $5}' /proc/locks)
for p in $(pgrep -f '[a]gent-sessions-cli.* serve --stdio'); do
  role=reader
  for w in $writers; do [ "$p" = "$w" ] && role=writer; done
  echo $role
done`)
	roles := strings.Fields(out)
	slices.Sort(roles)
	return roles
}

func waitRoles(t *testing.T, h *sshtest.Host, want []string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := serveRoles(t, h)
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote server roles = %v, want %v", got, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Two clients on one host each get their own server. They share one index:
// the first is the writer, the second reads it, and takes over as writer
// when the first disconnects.
func TestSSHD_TwoClientsShareOneIndex(t *testing.T) {
	h := startSSHD(t)
	alias := h.Alias(sshtest.UserSh)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Separate control masters, as two desktop apps would have.
	opts1 := sshdOpts(h)
	cmDir, err := os.MkdirTemp("", "as-cm2")
	if err != nil {
		t.Fatal(err)
	}
	opts2 := SSHOptions{ConfigFile: h.ConfigFile, ControlPath: filepath.Join(cmDir, "%C")}
	t.Cleanup(func() {
		_ = StopControlMaster(context.Background(), "", alias, opts2)
		_ = os.RemoveAll(cmDir)
	})

	sess1, err := StartSession(ctx, alias, opts1, nil, noopEmitter())
	if err != nil {
		t.Fatalf("first client: %v", err)
	}
	defer func() { _ = sess1.Close() }()
	browseFixture(ctx, t, sess1)
	waitRoles(t, h, []string{"writer"}, 10*time.Second)

	sess2, err := StartSession(ctx, alias, opts2, nil, noopEmitter())
	if err != nil {
		t.Fatalf("second client: %v", err)
	}
	defer func() { _ = sess2.Close() }()
	if sess2.bin != sess1.bin {
		t.Errorf("second client runs %q, first %q", sess2.bin, sess1.bin)
	}
	browseFixture(ctx, t, sess2)
	waitRoles(t, h, []string{"reader", "writer"}, 10*time.Second)
	browseFixture(ctx, t, sess1)

	_ = sess1.Close()
	waitRoles(t, h, []string{"writer"}, 20*time.Second)
	browseFixture(ctx, t, sess2)

	_ = sess2.Close()
	waitServeCount(t, h, 0, 15*time.Second)
}

// bundleVariant writes the server bundle in dir again with a different gzip
// header: same binary and version, another sha, as a rebuild would give.
func bundleVariant(t *testing.T, dir, name string) string {
	t.Helper()
	file := "agent-sessions-cli-linux-" + runtime.GOARCH + ".gz"
	src, err := os.Open(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	zr, err := gzip.NewReader(src)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	dst, err := os.Create(filepath.Join(out, file))
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(dst)
	zw.Comment = name
	if _, err := io.Copy(zw, zr); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

// bundleTag is the build dir a deploy of the bundle in dir installs.
func bundleTag(t *testing.T, dir string) string {
	t.Helper()
	sum, err := fileSHA256(filepath.Join(dir, "agent-sessions-cli-linux-"+runtime.GOARCH+".gz"))
	if err != nil {
		t.Fatal(err)
	}
	return ServerDirTag(version.Current(), sum)
}

// A rebuild of the same version deploys into its own directory and leaves
// the other builds alone; a deploy prunes only the builds nobody has started
// for PruneAfter.
func TestSSHD_RebuildDeploysBesideAndPrunesUnused(t *testing.T) {
	h := startSSHD(t)
	user := sshtest.UserSh
	alias := h.Alias(user)
	opts := sshdOpts(h)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	start := func(dir string) {
		t.Helper()
		t.Setenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR", dir)
		sess, err := StartSession(ctx, alias, opts, nil, noopEmitter())
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if want := RemoteServerPath("/home/"+user, bundleTag(t, dir)); sess.bin != want {
			t.Errorf("server = %q, want %q", sess.bin, want)
		}
		browseFixture(ctx, t, sess)
		_ = sess.Close()
	}
	builds := func() []string {
		t.Helper()
		return strings.Fields(h.ExecAs(t, user, `cd "$HOME/.cache/agent-sessions/server" && ls -1`))
	}
	backdate := func(tags ...string) {
		t.Helper()
		for _, tag := range tags {
			h.ExecAs(t, user, `touch -d '2000-01-01 00:00:00' "$HOME/.cache/agent-sessions/server/"`+QuotePOSIX(tag))
		}
	}

	dirA := sshtest.ServerBundleDir(t, version.Current())
	dirB := bundleVariant(t, dirA, "b")
	dirC := bundleVariant(t, dirA, "c")
	a, b, c := bundleTag(t, dirA), bundleTag(t, dirB), bundleTag(t, dirC)
	if a == b || b == c || a == c {
		t.Fatalf("variants share a tag: %s %s %s", a, b, c)
	}

	start(dirA)
	start(dirB)
	if got, want := builds(), sortedStrings(a, b); !slices.Equal(got, want) {
		t.Fatalf("after rebuild: builds %v, want %v", got, want)
	}

	// Both builds look unused; starting b again marks it in use.
	backdate(a, b)
	start(dirB)
	start(dirC)
	if got, want := builds(), sortedStrings(b, c); !slices.Equal(got, want) {
		t.Fatalf("after prune: builds %v, want %v", got, want)
	}

	// A build pruned after the probe listed it is reported as missing, so
	// StartSession deploys it again instead of failing.
	h.ExecAs(t, user, `rm -rf "$HOME/.cache/agent-sessions/server/"`+QuotePOSIX(b))
	sess, err := DialSession(ctx, alias, opts, RemoteServerPath("/home/"+user, b), nil, noopEmitter())
	if err == nil {
		_ = sess.Close()
	}
	if !errors.Is(err, ErrServerMissing) {
		t.Fatalf("start of a pruned build: err = %v, want ErrServerMissing", err)
	}
}

func sortedStrings(s ...string) []string {
	slices.Sort(s)
	return s
}
