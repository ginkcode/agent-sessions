//go:build windows

package term

import (
	"os"
	"os/exec"

	"github.com/aymanbagabas/go-pty"
)

func afterStart(pty.Pty) {}

// terminate closes the pseudoconsole, which ends the processes attached to
// it, and then kills the process. The reader keeps draining meanwhile:
// closing the pseudoconsole waits for its output to be read.
func (s *session) terminate() {
	s.killOnce.Do(func() {
		s.unpause()
		go func() {
			s.closePty()
			_ = s.cmd.Process.Kill()
		}()
	})
}

func stateCode(state *os.ProcessState) int { return state.ExitCode() }

func detachShell(*exec.Cmd) {}
