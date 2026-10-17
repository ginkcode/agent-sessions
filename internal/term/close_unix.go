//go:build !windows

package term

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// afterStart closes the parent's copy of the terminal side, so reading
// ends once the process and everything it started let go of it.
func afterStart(p pty.Pty) {
	if u, ok := p.(pty.UnixPty); ok {
		_ = u.Slave().Close()
	}
}

// terminate hangs up the process group (the process leads its own session)
// and kills it if it is still running after killGrace.
func (s *session) terminate() {
	s.killOnce.Do(func() {
		s.unpause()
		pgid := s.cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGHUP)
		go func() {
			select {
			case <-s.exited:
			case <-time.After(s.killGrace):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}()
	})
}

// stateCode is the exit status, or 128 plus the signal that ended the
// process, as shells report it.
func stateCode(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return state.ExitCode()
}

// detachShell runs cmd in its own session, without a controlling terminal,
// so an interactive shell cannot take over the one the app was started
// from.
func detachShell(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
