//go:build !windows

package files

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
