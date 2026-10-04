//go:build windows

package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

const terminalSupported = true

// startTerminal runs script in a new console of the system Windows
// PowerShell, which every Windows 10/11 machine has. Windows 11 shows the
// console in Windows Terminal when that is the default terminal. -NoExit
// keeps the window, and any error, after the agent exits. -Command is not
// subject to the execution policy, so no policy override is needed.
func startTerminal(script, dir string) error {
	ps, err := windowsPowerShell()
	if err != nil {
		return err
	}
	cmd := exec.Command(ps, "-NoLogo", "-NoExit", "-Command", script)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// windowsPowerShell returns the system powershell.exe, never whichever one a
// GUI launch finds on PATH.
func windowsPowerShell() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New("Windows PowerShell was not found at " + path)
	}
	return path, nil
}
