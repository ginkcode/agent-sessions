//go:build windows

package app

import (
	"context"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/remote"
)

func TestWindowsConnectionNoAskpassOrMaster(t *testing.T) {
	c := newConnection(nil)
	defer c.Shutdown()
	if c.askpass != nil || c.opts.AskpassBinary != "" || c.opts.AskpassSock != "" || c.opts.AskpassToken != "" || c.opts.ControlMaster != "no" {
		t.Fatalf("unsafe Windows connection defaults: broker=%t master=%q", c.askpass != nil, c.opts.ControlMaster)
	}
	c.stopMaster = func(context.Context, string, remote.SSHOptions) error {
		t.Error("Windows tried to stop a Unix master")
		return nil
	}
	c.releaseHost("box", c.opts)
}
