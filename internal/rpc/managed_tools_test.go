package rpc

import (
	"context"
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
