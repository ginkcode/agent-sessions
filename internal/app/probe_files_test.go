package app

import (
	"context"
	"testing"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type probeBackend struct {
	*stubRemoteBackend
	run   func()
	calls int
}

func (b *probeBackend) ProbeFiles(context.Context, string, []string) (files.ProbeResult, error) {
	b.calls++
	if b.run != nil {
		b.run()
	}
	return files.ProbeResult{Observations: []files.DiskObservation{{Path: "a", State: "absent"}}}, nil
}

func TestProbeHostEpoch(t *testing.T) {
	a := setupRemoteApp(&stubRemoteBackend{})
	b := &probeBackend{stubRemoteBackend: &stubRemoteBackend{}}
	a.conn.client = b
	if _, err := a.ProbeFiles("/repo", []string{"a"}, ""); err == nil || b.calls != 0 {
		t.Fatal("wrong host probe dispatched")
	}
	b.run = func() { a.conn.mu.Lock(); a.conn.state.Generation++; a.conn.mu.Unlock() }
	if out, err := a.ProbeFiles("/repo", []string{"a"}, "remote-server"); err == nil || len(out.Observations) != 0 {
		t.Fatalf("stale epoch accepted: %+v %v", out, err)
	}
	b.run = nil
	if out, err := a.ProbeFiles("/repo", []string{"a"}, "remote-server"); err != nil || out.Observations[0].State != "absent" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestInvalidateDiagnosticsRouteFailsClosed(t *testing.T) {
	a := setupRemoteApp(&stubRemoteBackend{})
	a.conn.client = &stubRemoteBackend{}
	if _, err := a.InvalidateCodeDiagnosticsPath(cd.Context{}, "a", "remote-server"); err == nil {
		t.Fatal("unsupported backend accepted invalidation")
	}
}
