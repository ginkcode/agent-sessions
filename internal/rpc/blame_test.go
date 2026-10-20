package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestBlameFileRPC(t *testing.T) {
	mock := &mockBackend{}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()
	ctx := context.Background()
	init, err := client.Initialize(ctx, InitializeRequest{})
	if err != nil || !init.Capabilities.Blame {
		t.Fatalf("initialize = %+v, %v", init, err)
	}
	got, err := client.BlameFile(ctx, "/repo", "file.txt", 7)
	want := files.BlameLine{Line: 7, Commit: "abc123", Author: "Test Author", AuthorTime: 1700000000, Summary: "/repo/file.txt"}
	if err != nil || got != want {
		t.Fatalf("blame = %+v, %v; want %+v", got, err, want)
	}
	mock.errToReturn = engine.ErrUnknownDirectory
	if _, err := client.BlameFile(ctx, "/unknown", "file.txt", 1); err == nil || !strings.Contains(err.Error(), engine.ErrUnknownDirectory.Error()) {
		t.Fatalf("backend error = %v", err)
	}
}

func TestBlameFileRPCUnsupported(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	client := NewClient(r, rec)
	defer client.Close()
	if _, err := client.BlameFile(context.Background(), "/repo", "file", 1); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Fatalf("unsupported error = %v", err)
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported call wrote to transport")
	}
	raw, err := json.Marshal(Capabilities{})
	if err != nil || strings.Contains(string(raw), "blame") {
		t.Fatalf("optional capability = %s, %v", raw, err)
	}
	var caps Capabilities
	if err := json.Unmarshal([]byte(`{"search":true}`), &caps); err != nil || caps.Blame {
		t.Fatalf("older capability = %+v, %v", caps, err)
	}
}
