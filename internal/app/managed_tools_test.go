package app

import (
	"context"
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

func TestManagedAppRealEngineUnavailable(t *testing.T) {
	e := engine.NewEngine(engine.WithCacheEnabled(false), engine.WithRoots(paths.Roots{Config: t.TempDir()}))
	defer func() { _ = e.Close() }()
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{backend: e}
	out, err := a.ManagedTool(engine.ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "gopls"})
	if err != nil || out.State != "unavailable" || out.Proposal != nil || e.LiveCodeDiagnosticsReady() {
		t.Fatalf("%+v %v", out, err)
	}
}
