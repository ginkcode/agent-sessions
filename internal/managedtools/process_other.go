//go:build !darwin && !linux

package managedtools

import (
	"os"
	"os/exec"
)

// New rejects these platforms before any command is reachable.
func isolateCommand(_ *exec.Cmd) {}

func ownedByUser(os.FileInfo) bool { return false }
