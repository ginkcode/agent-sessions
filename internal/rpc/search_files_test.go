package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestSearchFilesRPC(t *testing.T) {
	mock := &mockBackend{}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()
	ctx := context.Background()
	init, err := client.Initialize(ctx, InitializeRequest{})
	if err != nil || !init.Capabilities.SearchFiles {
		t.Fatalf("initialize = %+v, %v", init, err)
	}
	got, err := client.SearchFiles(ctx, "/repo", files.SearchQuery{Pattern: "needle", Include: "*.go"})
	if err != nil || got.Matches != 1 || len(got.Files) != 1 {
		t.Fatalf("search = %+v, %v", got, err)
	}
	if f := got.Files[0]; f.Path != "/repo/*.go" || f.Revision != "r1" || f.Matches[0].Preview != "needle" || f.Matches[0].End != 6 {
		t.Fatalf("query did not round-trip: %+v", f)
	}
	mock.errToReturn = engine.ErrUnknownDirectory
	if _, err := client.SearchFiles(ctx, "/unknown", files.SearchQuery{Pattern: "x"}); err == nil || !strings.Contains(err.Error(), engine.ErrUnknownDirectory.Error()) {
		t.Fatalf("backend error = %v", err)
	}
}

func TestSearchFilesRPCCancel(t *testing.T) {
	mock := &mockBackend{slowCalled: make(chan struct{})}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()
	if _, err := client.Initialize(context.Background(), InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.SearchFiles(ctx, "/repo", files.SearchQuery{Pattern: "block"})
		done <- err
	}()
	select {
	case <-mock.slowCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("search never reached the server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the search")
	}
}

func TestSearchFilesRPCUnsupported(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	client := NewClient(r, rec)
	defer client.Close()
	if _, err := client.SearchFiles(context.Background(), "/repo", files.SearchQuery{Pattern: "x"}); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Fatalf("unsupported error = %v", err)
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported call wrote to transport")
	}
	raw, err := json.Marshal(Capabilities{})
	if err != nil || strings.Contains(string(raw), "searchFiles") {
		t.Fatalf("optional capability = %s, %v", raw, err)
	}
}
