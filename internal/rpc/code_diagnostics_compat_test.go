package rpc

import (
	"context"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestDiagnosticsAbsentOldFlagsStillBrowseEdit(t *testing.T) {
	s, c, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return &mockBackend{}, Capabilities{SaveFile: true}, paths.Roots{}, nil
	})
	ctx := context.Background()
	init, e := c.Initialize(ctx, InitializeRequest{})
	if e != nil || init.Capabilities.CodeDiagnostics || init.Capabilities.LiveCodeDiagnostics {
		t.Fatal(init, e)
	}
	if _, e = c.ListFiles(ctx, "/repo"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReadFile(ctx, "/repo", "file.txt"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.SaveFile(ctx, "/repo", files.SaveRequest{}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.AttachCodeDiagnostics(ctx, "/repo", 1); e == nil {
		t.Fatal("old server unexpectedly supports checks")
	}
}
