package rpc

import (
	"context"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestResolveTerminalFileRPC(t *testing.T) {
	mock := &mockBackend{}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()
	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	// Opaque host-native paths must survive transport without desktop-side
	// normalization (including Windows paths on a Unix desktop).
	want := files.TerminalFile{Dir: `C:\work\project`, Path: `sub\Makefile`}
	got, err := client.ResolveTerminalFile(ctx, want.Dir, want.Path)
	if err != nil || got != want {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, want)
	}
	mock.errToReturn = engine.ErrUnknownDirectory
	if _, err := client.ResolveTerminalFile(ctx, "/unauthorized", "file"); err == nil || !strings.Contains(err.Error(), engine.ErrUnknownDirectory.Error()) {
		t.Fatalf("authorization error: %v", err)
	}
}
