//go:build windows

package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// Use the native client, not whichever Git/MSYS ssh a GUI launch finds on PATH.
func defaultSSHBinary() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", ErrSSHClientNotFound
	}
	path := filepath.Join(dir, "OpenSSH", "ssh.exe")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", ErrSSHClientNotFound
	}
	return path, nil
}

func prepareSSHCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
