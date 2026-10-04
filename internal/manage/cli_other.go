//go:build !windows

package manage

import "os/exec"

func hideConsole(*exec.Cmd) {}
