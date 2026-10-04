//go:build windows

package manage

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole keeps a provider CLI from opening a console window: the GUI
// has no console, so Windows would give the CLI a new, visible one. The CLI
// still gets a console, without a window, and its children share it.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
