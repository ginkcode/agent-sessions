//go:build windows

package claude

import (
	"errors"

	"golang.org/x/sys/windows"
)

// pidExists reports whether pid is a running process. A process that exists
// but cannot be queried counts as running, so a session is never declared
// idle on missing evidence.
func (p *Provider) pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// processStartTime is not comparable with Claude Code's procStart on
// Windows; pidMatchesProcStart uses PID existence instead, so a reused PID
// keeps a session live rather than idle.
func processStartTime(string, int) int64 { return 0 }

const stillActive = 259 // STILL_ACTIVE (STATUS_PENDING)
