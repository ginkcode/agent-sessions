package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"io"
	"reflect"
	"testing"
)

type gitStatusBackend struct {
	*mockBackend
	dir string
}

func (b *gitStatusBackend) GitStatus(ctx context.Context, dir string) (files.RepoStatus, error) {
	b.dir = dir
	return files.RepoStatus{OK: true, Branch: "main", Entries: []files.GitChange{{Path: "new", OriginalPath: "old", Index: "R", Worktree: "M"}}}, b.errToReturn
}
func TestGitStatusRPC(t *testing.T) {
	backend := &gitStatusBackend{mockBackend: &mockBackend{}}
	_, client, cleanup := setupPipePair(backend)
	defer cleanup()
	init, err := client.Initialize(context.Background(), InitializeRequest{})
	if err != nil || !init.Capabilities.GitChanges {
		t.Fatalf("init: %+v %v", init, err)
	}
	got, err := client.GitStatus(context.Background(), "/repo/sub")
	want, _ := backend.GitStatus(context.Background(), "/repo/sub")
	if err != nil || !reflect.DeepEqual(got, want) || backend.dir != "/repo/sub" {
		t.Fatalf("status: %+v %v", got, err)
	}
	backend.errToReturn = engine.ErrUnknownDirectory
	if _, err = client.GitStatus(context.Background(), "/unknown"); err == nil {
		t.Fatal("lost error")
	}
}
func TestGitStatusUnsupported(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	client := NewClient(r, rec)
	defer client.Close()
	if _, err := client.GitStatus(context.Background(), "/repo"); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Fatal("wrote unsupported request")
	}
	raw, _ := json.Marshal(Capabilities{})
	var caps map[string]any
	json.Unmarshal(raw, &caps)
	if _, ok := caps["gitChanges"]; ok {
		t.Fatal("optional capability serialized")
	}
	_, client2, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	client2.Initialize(context.Background(), InitializeRequest{})
	if _, err := client2.GitStatus(context.Background(), "/repo"); err == nil {
		t.Fatal("backend without optional extension accepted")
	}
}
