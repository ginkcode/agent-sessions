//go:build darwin || linux

package codediagnostics

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
)

// HostSessionLaunch starts an approved language server on this host. It is the
// production SessionLaunch: the SessionManager calls it only after known-root,
// composite-approval and lease validation, with the adapter's fixed argv and
// complete environment. The server gets its own process group so Terminate
// kills the whole tree. Nothing is downloaded or resolved through PATH here:
// Executable is the absolute, fingerprinted path.
func HostSessionLaunch(in Invocation) (SessionProcess, SessionStreams, error) {
	if in.Executable == "" || in.Executable[0] != '/' {
		return nil, SessionStreams{}, failure("invalidArgument", "Language server executable must be absolute")
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, SessionStreams{}, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW)
		return nil, SessionStreams{}, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW)
		return nil, SessionStreams{}, err
	}
	cmd := exec.Command(in.Executable, in.Args...)
	cmd.Dir, cmd.Env = in.Directory, in.Env
	// *os.File streams are inherited directly: no exec copy goroutines, so Wait
	// never races the transport's readers.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stderrW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW)
		return nil, SessionStreams{}, err
	}
	// Child ends belong to the child now.
	closeAll(stdinR, stdoutW, stderrW)
	p := &hostSession{cmd: cmd, files: []*os.File{stdoutR, stderrR}}
	return p, SessionStreams{Stdin: stdinW, Stdout: stdoutR, Stderr: stderrR}, nil
}

type hostSession struct {
	cmd    *exec.Cmd
	files  []*os.File
	once   sync.Once
	exited atomic.Bool
}

func (p *hostSession) Wait() (int, error) {
	err := p.cmd.Wait()
	p.exited.Store(true)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (p *hostSession) Terminate() error {
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	// macOS reports EPERM for a group whose only members are killed processes
	// not yet reaped by their new parent. After the leader exited, nothing is
	// left that this account could signal.
	if errors.Is(err, syscall.EPERM) && p.exited.Load() {
		return nil
	}
	return err
}

// Close releases the parent's read ends; a descendant that kept the pipes open
// cannot keep the transport readers blocked.
func (p *hostSession) Close() error {
	p.once.Do(func() { closeAll(p.files...) })
	return nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
