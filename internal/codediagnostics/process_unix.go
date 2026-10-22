//go:build darwin || linux

package codediagnostics

import (
	"errors"
	"os/exec"
	"syscall"
)

type hostProcess struct{ cmd *exec.Cmd }

func startProcess(in Invocation) (Process, error) {
	cmd := exec.Command(in.Executable, in.Args...)
	cmd.Dir, cmd.Env = in.Directory, in.Env
	cmd.Stdout, cmd.Stderr = in.Stdout, in.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bounds pipe draining even when an uncooperative descendant keeps a pipe open.
	cmd.WaitDelay = in.CleanupTimeout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &hostProcess{cmd}, nil
}

func (p *hostProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (p *hostProcess) Terminate() error {
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func (p *hostProcess) Close() error { return nil }
