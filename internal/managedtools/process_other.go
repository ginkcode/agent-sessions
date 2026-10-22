//go:build !darwin && !linux

package managedtools

import "os/exec"

// New rejects these platforms before any command is reachable.
func isolateCommand(_ *exec.Cmd) {}
