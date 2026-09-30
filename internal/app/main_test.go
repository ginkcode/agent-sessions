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
	os.Exit(m.Run())
}
