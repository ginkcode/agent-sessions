package remote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

	stderrMu sync.Mutex
	stderr   []byte

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
	probe, err := ProbeHost(ctx, alias, opts, ServerDirTag(version.Current(), ""))
	if err != nil {
		return nil, err
	}

	bin := probe.ServerPath
	if probe.InstalledVersion == "" || bin == "" {
		bin, err = DeployServer(ctx, alias, opts, probe, "")
		if err != nil {
			return nil, err
		}
	}
	return startServe(ctx, alias, opts, bin, clientEnv, emitter)
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

// handshakeTimeout bounds the preface scan plus initialize. The ssh master
// is already up from the probe, so this covers only the remote login shell
// and server startup.
var handshakeTimeout = 60 * time.Second

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
	// has written any banner, so WaitForPreface can skip it.
	script := fmt.Sprintf(`%s serve --stdio --nonce %s`, QuotePOSIX(bin), QuotePOSIX(nonce))
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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start remote server: %w", err)
	}

	s := &Session{
		alias:  alias,
		opts:   opts,
		bin:    bin,
		cancel: cancel,
		cmd:    cmd,
		done:   make(chan struct{}),
	}
	go s.drainStderr(stderr)
	go s.wait()

	// Bound the handshake: a login shell that stalls, or streams output with
	// no preface, must not leave the connect hanging. Cancelling runCtx
	// kills ssh, which ends the preface scan or the initialize call.
	handshake := time.AfterFunc(handshakeTimeout, cancel)
	defer handshake.Stop()
	framed, err := rpc.WaitForPreface(runCtx, stdout, nonce)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("remote server handshake: %w%s", handshakeErr(ctx, runCtx, err), s.stderrSuffix())
	}

	client := rpc.NewClient(framed, stdin)
	if emitter != nil {
		client.SetEmitter(emitter)
	}
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

func (s *Session) drainStderr(r io.Reader) {
	br := bufio.NewReader(r)
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			s.stderrMu.Lock()
			s.stderr = append(s.stderr, buf[:n]...)
			if len(s.stderr) > stderrCap {
				s.stderr = s.stderr[len(s.stderr)-stderrCap:]
			}
			s.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) stderrSuffix() string {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	if len(s.stderr) == 0 {
		return ""
	}
	return " (stderr: " + string(s.stderr) + ")"
}

// Stderr returns the remote stderr retained so far. It is for diagnostics;
// askpass answers never travel on this stream.
func (s *Session) Stderr() string {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	return string(s.stderr)
}

func (s *Session) wait() {
	s.waitOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
		close(s.done)
	})
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
