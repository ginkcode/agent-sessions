//go:build darwin || linux

package files

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Kill the Git process group on timeout so hooks cannot continue writing after
// the caller has observed cancellation and released the repository write lock.
func prepareGitWriteProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
