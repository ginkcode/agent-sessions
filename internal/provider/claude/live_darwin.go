//go:build darwin

package claude

import "syscall"

// pidExists uses the portable process signal check. Signal zero performs the
// permission/existence test without sending a signal.
func (p *Provider) pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processStartTime has no portable macOS equivalent for Linux stat field 22.
// Returning zero makes a recorded procStart fail closed rather than matching
// an unrelated recycled PID.
func processStartTime(string, int) int64 { return 0 }
