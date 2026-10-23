package rpc

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestManagedToolsPipeFailsClosedOldFlagsAndUnverifiedIdentity(t *testing.T) {
	s, c, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return &mockBackend{}, Capabilities{SaveFile: true}, paths.Roots{}, nil
	})
	ctx := context.Background()
	init, err := c.Initialize(ctx, InitializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if init.Capabilities.LiveCodeDiagnostics {
		t.Fatal("live enabled")
	}
	if _, err = c.ManagedTool(ctx, engine.ManagedToolRequest{Action: "propose", ExpectedHostID: "local:fake", Tool: "gopls"}); err == nil {
		t.Fatal("old peer supported")
	}
	for _, params := range []any{
		engine.ManagedToolRequest{Action: "install", ExpectedHostID: "local:fake", Token: "consent"},
		map[string]any{"action": "propose", "url": "https://evil", "path": "/tmp/evil", "hash": "fake"},
		map[string]any{"action": "status", "token": strings.Repeat("x", 4096)},
	} {
		var out engine.ManagedToolState
		if err = c.call(ctx, "managedTool", params, &out); err == nil {
			t.Fatal("remote install allowed")
		}
	}
	if _, err = c.ListFiles(ctx, "/repo"); err != nil {
		t.Fatal("optional refusal broke browsing", err)
	}
}

type managedBackend struct {
	*diagnosticsBackend
	requests []engine.ManagedToolRequest
}

func (b *managedBackend) ManagedTool(_ context.Context, p engine.ManagedToolRequest) (engine.ManagedToolState, error) {
	b.requests = append(b.requests, p)
	return engine.ManagedToolState{HostID: b.host, State: "idle", Tools: map[string]string{"ruff": "missingTool"}}, nil
}

// A remote server with its own verified identity forwards to its engine, which
// installs on that host; the request is checked against the identity on both
// ends.
func TestManagedToolsForwardedToVerifiedRemoteHost(t *testing.T) {
	d, _, _, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	b := &managedBackend{diagnosticsBackend: d}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s, c := NewServer(b, inR, outW), NewClient(outR, inW)
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(context.Background()) }()
	defer func() { _ = c.Close(); _ = s.Close(); <-done }()
	ctx := context.Background()
	if _, err := c.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	out, err := c.ManagedTool(ctx, engine.ManagedToolRequest{Action: "status", ExpectedHostID: d.host})
	if err != nil || out.State != "idle" || out.Tools["ruff"] != "missingTool" || len(b.requests) != 1 {
		t.Fatal(out, err)
	}
	if _, err = c.ManagedTool(ctx, engine.ManagedToolRequest{Action: "status", ExpectedHostID: "ssh:other"}); err == nil {
		t.Fatal("client accepted another host")
	}
	var raw engine.ManagedToolState
	for _, params := range []any{
		engine.ManagedToolRequest{Action: "status", ExpectedHostID: "ssh:other"},
		map[string]any{"action": "propose", "expectedHostId": d.host, "tool": "gopls", "url": "https://evil"},
	} {
		if err = c.call(ctx, "managedTool", params, &raw); err == nil {
			t.Fatal("server accepted", params)
		}
	}
	if len(b.requests) != 1 {
		t.Fatal("rejected request reached the engine")
	}
}
