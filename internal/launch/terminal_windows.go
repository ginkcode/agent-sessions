//go:build windows

package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const terminalSupported = true

// detach is a no-op: terminal apps are only started on Linux and macOS.
func detach(*exec.Cmd) {}

// startTerminal runs script in a new console of the system Windows
// PowerShell, which every Windows 10/11 machine has. Windows 11 shows the
// console in Windows Terminal when that is the default terminal. -NoExit
// keeps the window, and any error, after the agent exits. -Command is not
// subject to the execution policy, so no policy override is needed.
func startTerminal(script, dir string) error {
	h, err := startConsole(script, dir, windows.CREATE_NEW_CONSOLE)
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}

// startConsole starts PowerShell with script in dir and returns its process
// handle.
func startConsole(script, dir string, flags uint32) (windows.Handle, error) {
	ps, err := windowsPowerShell()
	if err != nil {
		return 0, err
	}
	return startProcess(ps, []string{ps, "-NoLogo", "-NoExit", "-Command", script}, dir, flags)
}

// startProcess starts app with argv in dir and returns its process handle.
// os/exec cannot be used: it always passes standard handles (NUL when
// unset), so the shell and the agent would read NUL instead of the new
// console, and PowerShell exits at once despite -NoExit. Without
// STARTF_USESTDHANDLES the child uses its own console.
func startProcess(app string, argv []string, dir string, flags uint32) (windows.Handle, error) {
	appPtr, err := windows.UTF16PtrFromString(app)
	if err != nil {
		return 0, err
	}
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return 0, err
	}
	cwd, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	si := &windows.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(appPtr, cmdline, nil, nil, false, flags|windows.CREATE_UNICODE_ENVIRONMENT, nil, cwd, si, &pi); err != nil {
		return 0, err
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}

// OpenWSLTerminal opens a new console running script with /bin/sh in
// distro. The script, from PosixTerminalScript, enters the session's
// directory itself and ends in the user's shell, so the window stays as
// -NoExit keeps PowerShell's.
func OpenWSLTerminal(distro, script string) error {
	wsl, err := WSLBinary()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	h, err := startProcess(wsl, append([]string{wsl}, WSLExecArgs(distro, script)...), home, windows.CREATE_NEW_CONSOLE)
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}

// WSLBinary returns the system wsl.exe, or ErrWSLNotInstalled.
func WSLBinary() (string, error) {
	notInstalled := fmt.Errorf("%w; install it with 'wsl --install' in a terminal", ErrWSLNotInstalled)
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", notInstalled
	}
	path := filepath.Join(dir, "wsl.exe")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", notInstalled
	}
	return path, nil
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
