//go:build windows

package files

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Git is a background helper for the viewer, not an interactive terminal.
// A GUI process without a console would otherwise give git.exe a visible one.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
