package remote

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// stderrCap is how much remote stderr is retained for error reporting.
const stderrCap = 64 * 1024

// Session is one running `agent-sessions-cli serve --stdio` over ssh.
// Close is safe to call more than once.
type Session struct {
	alias  string
	opts   SSHOptions
	bin    string
	cancel context.CancelFunc
	cmd    *exec.Cmd

	client *rpc.Client
	init   *rpc.InitializeResult

	sshCmdFunc func(ctx context.Context, alias string, remoteCmd []string, opts SSHOptions) (*exec.Cmd, error)

	stderr sshStderr

	waitOnce sync.Once
	waitErr  error
	doneOnce sync.Once
	done     chan struct{}
}

// StartSession probes alias, deploys the bundled server when the expected
// build is not already installed, starts it inside a login shell, and
// completes the JSON-RPC initialize handshake. clientEnv is filtered to the
// server's allowlist by the server itself.
func StartSession(ctx context.Context, alias string, opts SSHOptions, clientEnv map[string]string, emitter engine.Emitter) (*Session, error) {
	if err := ValidateHostAlias(alias); err != nil {
		return nil, err
	}
	probe, err := ProbeHost(ctx, alias, opts, version.Current())
	if err != nil {
		return nil, err
	}

	bin, bundle := chooseServer(probe)
	if bin == "" {
		bin, err = DeployServer(ctx, alias, opts, probe, bundle)
		if err != nil {
			return nil, err
		}
		return startServe(ctx, alias, opts, bin, clientEnv, emitter)
	}
	sess, err := startServe(ctx, alias, opts, bin, clientEnv, emitter)
	if !errors.Is(err, ErrServerMissing) {
		return sess, err
	}
	// Another client's deploy pruned the build after the probe listed it.
	bin, err = DeployServer(ctx, alias, opts, probe, bundle)
	if err != nil {
		return nil, err
	}
	return startServe(ctx, alias, opts, bin, clientEnv, emitter)
}

// ErrServerMissing means the server binary was gone when the start script
// ran, as when a prune removed it after the probe.
var ErrServerMissing = errors.New("remote server build is missing")

// exitServerMissing is the start script's exit status for ErrServerMissing.
const exitServerMissing = 87

// chooseServer returns the installed build to run, or "" and the local
// bundle to deploy. The build must be this bundle's own, matched by
// checksum: a rebuild with the same version is redeployed rather than
// running the old one. Builds are never replaced, so a build another client
// runs stays as it is. Without a readable local bundle, the newest build of
// this version is the best there is.
func chooseServer(probe *HostProbe) (bin, bundle string) {
	if gz, err := LocateServer(probe.OS, probe.Arch); err == nil {
		if sum, err := fileSHA256(gz); err == nil {
			tag := ServerDirTag(version.Current(), sum)
			if probe.Has(tag) {
				return RemoteServerPath(probe.Home, tag), ""
			}
			return "", gz
		}
	}
	if len(probe.Installed) > 0 {
		return RemoteServerPath(probe.Home, probe.Installed[0]), ""
	}
	return "", ""
}

// DialSession starts an already-installed server binary. Tests use it to skip
// probe and deploy.
func DialSession(ctx context.Context, alias string, opts SSHOptions, bin string, clientEnv map[string]string, emitter engine.Emitter) (*Session, error) {
	if err := ValidateHostAlias(alias); err != nil {
		return nil, err
	}
	if bin == "" {
		return nil, fmt.Errorf("remote server path is empty")
	}
	return startServe(ctx, alias, opts, bin, clientEnv, emitter)
}

// handshakeTimeout bounds SSH authentication, the remote login shell,
// preface scan and initialize. Windows authenticates again without a master.
var handshakeTimeout = 60 * time.Second

// heartbeatInterval paces the heartbeats that keep the remote server from
// exiting on its idle timeout (60s by default, see serve --idle-timeout).
// Several must fit in that window so a slow link does not trip it.
const heartbeatInterval = 15 * time.Second

// handshakeErr names the timeout when it, rather than the caller, ended the
// handshake.
func handshakeErr(parent, run context.Context, err error) error {
	if run.Err() != nil && parent.Err() == nil {
		return fmt.Errorf("no response within %v: %w", handshakeTimeout, err)
	}
	return err
}

func startServe(ctx context.Context, alias string, opts SSHOptions, bin string, clientEnv map[string]string, emitter engine.Emitter) (*Session, error) {
	nonce := rpc.GenerateNonce()
	// The preface is printed by the server (--nonce), after the login shell
	// has written any banner, so WaitForPreface can skip it. exec leaves no
	// shell behind to outlive the server.
	script := fmt.Sprintf(`exec %s serve --stdio --nonce %s`, QuotePOSIX(bin), QuotePOSIX(nonce))
	if dir, ok := buildDir(bin); ok {
		// Marks the build as in use, so no client's prune removes it.
		script = fmt.Sprintf(`touch -c %s 2>/dev/null; [ -x %s ] || exit %d; %s`,
			QuotePOSIX(dir), QuotePOSIX(bin), exitServerMissing, script)
	}
	opts.NoTTY = true

	runCtx, cancel := context.WithCancel(ctx)
	cmd, err := BuildSSHCmd(runCtx, alias, []string{LoginShell, "-lc", script}, opts)
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	s := &Session{
		alias:  alias,
		opts:   opts,
		bin:    bin,
		cancel: cancel,
		cmd:    cmd,
		done:   make(chan struct{}),
	}
	cmd.Stderr = &s.stderr
	// Cancellation must also release readers if a proxy child retains stdout.
	context.AfterFunc(runCtx, func() { _ = stdout.Close() })
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start remote server: %w", err)
	}
	go s.wait()

	// Bound the handshake: a login shell that stalls, or streams output with
	// no preface, must not leave the connect hanging. Cancelling runCtx
	// kills ssh, which ends the preface scan or the initialize call.
	handshake := time.AfterFunc(handshakeTimeout, cancel)
	defer handshake.Stop()
	framed, err := rpc.WaitForPreface(runCtx, stdout, nonce)
	if err != nil {
		// A script that exited on its own has ended ssh too, or soon will;
		// its status is read before Close kills ssh.
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
		missing := s.exitCode() == exitServerMissing
		_ = s.Close()
		if missing {
			return nil, fmt.Errorf("%w: %s", ErrServerMissing, bin)
		}
		if failure := sshFailure(s.waitErr, s.Stderr()); failure != nil && (errors.Is(failure, ErrSSHAuthentication) || errors.Is(failure, ErrSSHHostKey)) {
			return nil, fmt.Errorf("remote server handshake: %w", failure)
		}
		return nil, fmt.Errorf("remote server handshake: %w%s", handshakeErr(ctx, runCtx, err), s.stderrSuffix())
	}

	client := rpc.NewClient(framed, stdin)
	if emitter != nil {
		client.SetEmitter(emitter)
	}
	// Started before initialize: the server's idle timer runs from the
	// preface on, and starting the engine can take a while.
	go heartbeat(runCtx, client, heartbeatInterval)
	res, err := client.Initialize(runCtx, rpc.InitializeRequest{
		ProtocolVersion: rpc.ProtocolVersion,
		AppVersion:      version.Current(),
		ClientEnv:       clientEnv,
	})
	if err != nil {
		_ = client.Close()
		_ = s.Close()
		return nil, fmt.Errorf("remote initialize: %w%s", handshakeErr(ctx, runCtx, err), s.stderrSuffix())
	}
	s.client = client
	s.init = res
	go s.watchClient()
	return s, nil
}

// heartbeat runs until the session ends. A failed send needs no handling
// here: the same broken pipe ends the client's read loop and the session.
func heartbeat(ctx context.Context, client *rpc.Client, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			return
		case <-t.C:
			_ = client.Heartbeat()
		}
	}
}

func (s *Session) stderrSuffix() string {
	if msg := s.Stderr(); msg != "" {
		return " (stderr: " + msg + ")"
	}
	return ""
}

// Stderr returns the remote stderr retained so far. It is for diagnostics;
// askpass answers never travel on this stream.
func (s *Session) Stderr() string { return s.stderr.String() }

func (s *Session) wait() {
	s.waitOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
		close(s.done)
	})
}

// exitCode is the ssh exit status once the process has exited, else -1.
// ssh passes on the remote command's status.
func (s *Session) exitCode() int {
	select {
	case <-s.done:
	default:
		return -1
	}
	var exitErr *exec.ExitError
	if errors.As(s.waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// watchClient tears the ssh process down when the RPC reader hits EOF, so a
// remote exit becomes a single terminal state instead of a live-looking pipe.
func (s *Session) watchClient() {
	if s.client == nil {
		return
	}
	select {
	case <-s.client.Done():
		s.cancel()
	case <-s.done:
	}
}

// Client is the JSON-RPC backend for this session. It is nil before the
// handshake completes.
func (s *Session) Client() *rpc.Client { return s.client }

// NewTestSession builds a Session around an already-initialized client.
// The done channel is closed by Close. Production code uses StartSession.
func NewTestSession(alias string, client *rpc.Client, init *rpc.InitializeResult) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		alias:  alias,
		cancel: cancel,
		client: client,
		init:   init,
		done:   make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-clientDone(client):
		}
		s.closeDone()
	}()
	return s
}

func (s *Session) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

func clientDone(c *rpc.Client) <-chan struct{} {
	if c == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return c.Done()
}

// InitializeResult is the handshake payload. Nil before completion.
func (s *Session) InitializeResult() *rpc.InitializeResult { return s.init }

// Alias is the SSH host this session is connected to.
func (s *Session) Alias() string { return s.alias }

// Done is closed when the ssh process has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// Close cancels the ssh process, closes the RPC client, and waits for exit.
func (s *Session) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.cmd != nil && s.done != nil {
		<-s.done
	} else if s.done != nil {
		s.closeDone()
	}
	if s.waitErr != nil && !errors.Is(s.waitErr, context.Canceled) {
		// A killed process is the normal close path.
		return nil
	}
	return nil
}
