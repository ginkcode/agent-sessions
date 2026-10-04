//go:build !windows

package launch

import (
	"os/exec"
	"runtime"
	"syscall"
)

const terminalSupported = runtime.GOOS == "linux" || runtime.GOOS == "darwin"

func startTerminal(string, string) error { return ErrUnsupported }

// detach starts c in a new session, so a signal to the app's process group,
// such as Ctrl+C in the terminal running a development build, does not
// reach the terminal window.
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
