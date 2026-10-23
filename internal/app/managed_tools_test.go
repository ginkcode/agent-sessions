package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

type managedRouteBackend struct {
	engine.Backend
	host  string
	calls int
}

func (b *managedRouteBackend) CodeDiagnosticsHostID(context.Context) (string, error) {
	return b.host, nil
}

func (b *managedRouteBackend) ManagedTool(_ context.Context, p engine.ManagedToolRequest) (engine.ManagedToolState, error) {
	b.calls++
	return engine.ManagedToolState{HostID: b.host, State: "idle"}, nil
}

func TestManagedAppRouting(t *testing.T) {
	b := &managedRouteBackend{host: "local:actual"}
	a := &App{backend: b}
	if _, err := a.ManagedTool(engine.ManagedToolRequest{Action: "propose", ExpectedHostID: "picker-label", Tool: "gopls"}); err == nil || b.calls != 0 {
		t.Fatal("wrong host dispatched")
	}
	if _, err := a.ManagedTool(engine.ManagedToolRequest{Action: "status", ExpectedHostID: b.host}); err != nil || b.calls != 1 {
		t.Fatal(err)
	}
	a.conn = &connection{wantHost: "remote", localActive: false, state: ConnectionState{Phase: ConnDisconnected}}
	if _, err := a.ManagedTool(engine.ManagedToolRequest{Action: "install", ExpectedHostID: b.host, Token: "consent"}); err == nil || b.calls != 1 {
		t.Fatal("disconnect fell back to local")
	}
	a = &App{backend: &diagnosticsBackend{host: "old"}}
	if _, err := a.ManagedTool(engine.ManagedToolRequest{Action: "status", ExpectedHostID: "old"}); err == nil {
		t.Fatal("old backend supported")
	}
}

// A real engine answers a proposal with metadata only: no download, no app
// data written and no live route enabled.
func TestManagedAppRealEngineProposalOnly(t *testing.T) {
	data, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := engine.NewEngine(engine.WithCacheEnabled(false), engine.WithRoots(paths.Roots{Claude: t.TempDir(), Config: t.TempDir(), Data: data}), engine.WithFixtureMachineIdentity(testMachineID))
	defer func() { _ = e.Close() }()
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{backend: e}
	out, err := a.ManagedTool(engine.ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "ruff"})
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if err != nil || out.State != "unavailable" || out.Proposal != nil {
			t.Fatalf("%+v %v", out, err)
		}
		return
	}
	if err != nil || out.State != "proposed" || out.Proposal == nil || out.Proposal.HostID != host || e.LiveCodeDiagnosticsReady() {
		t.Fatalf("%+v %v", out, err)
	}
	if entries, _ := os.ReadDir(data); len(entries) != 0 {
		t.Fatal("proposal wrote app data")
	}
	if out, err = a.ManagedTool(engine.ManagedToolRequest{Action: "dismiss", ExpectedHostID: host}); err != nil || out.State != "idle" {
		t.Fatalf("%+v %v", out, err)
	}
}
