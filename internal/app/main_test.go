package app

import (
	"context"
	"os"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/remote"
)

func TestMain(m *testing.M) {
	// Connections built in unit tests must never run "ssh -O exit". The
	// sshd tests install the real stopper on their own connection.
	newMasterStopper = func(context.Context, string, remote.SSHOptions) error { return nil }
	// Real engines read the login shell's PATH; never run the developer's
	// shell and its startup files from tests.
	_ = os.Setenv("SHELL", "/usr/bin/true")
	os.Exit(m.Run())
}
