//go:build !windows

package remote

import "os/exec"

func defaultSSHBinary() (string, error) { return "ssh", nil }

func prepareSSHCommand(*exec.Cmd) {}
