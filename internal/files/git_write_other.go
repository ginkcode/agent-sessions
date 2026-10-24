//go:build !darwin && !linux

package files

import "os/exec"

func prepareGitWriteProcess(cmd *exec.Cmd) {}

func prepareGitSessionProcess(cmd *exec.Cmd) {}
