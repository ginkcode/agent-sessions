package rpc

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

type gitWriteMock struct {
	*mockBackend
	method, dir string
	paths       []string
	discard     files.GitDiscardRequest
	commit      files.GitCommitRequest
}

func (b *gitWriteMock) GitStage(_ context.Context, dir string, p []string) error {
	b.method = "stage"
	b.dir = dir
	b.paths = p
	return b.errToReturn
}
func (b *gitWriteMock) GitUnstage(_ context.Context, dir string, p []string) error {
	b.method = "unstage"
	b.dir = dir
	b.paths = p
	return b.errToReturn
}
func (b *gitWriteMock) GitDiscard(_ context.Context, dir string, p files.GitDiscardRequest) error {
	b.method = "discard"
	b.dir = dir
	b.discard = p
	return b.errToReturn
}
func (b *gitWriteMock) GitCommit(_ context.Context, dir string, p files.GitCommitRequest) error {
	b.method = "commit"
	b.dir = dir
	b.commit = p
	return b.errToReturn
}
func TestGitWriteRPC(t *testing.T) {
	b := &gitWriteMock{mockBackend: &mockBackend{}}
	s, c, cleanup := setupPipePair(b)
	defer cleanup()
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return b, Capabilities{SaveFile: true, GitWrite: true}, paths.Roots{}, nil
	})
	init, err := c.Initialize(context.Background(), InitializeRequest{})
	if err != nil || !init.Capabilities.GitWrite {
		t.Fatalf("init: %+v %v", init, err)
	}
	ctx := context.Background()
	p := []string{"a space", ":(glob)*"}
	if err := c.GitStage(ctx, "/repo", p); err != nil || b.method != "stage" || b.dir != "/repo" || !reflect.DeepEqual(b.paths, p) {
		t.Fatalf("stage %v %+v", err, b)
	}
	if err := c.GitUnstage(ctx, "/repo", nil); err != nil || b.method != "unstage" || len(b.paths) != 0 {
		t.Fatal(err)
	}
	discard := files.GitDiscardRequest{Paths: p, DraftPaths: []string{"draft"}}
	if err := c.GitDiscard(ctx, "/repo", discard); err != nil || !reflect.DeepEqual(b.discard, discard) {
		t.Fatal(err)
	}
	commit := files.GitCommitRequest{Message: "message\nbody", Amend: true, StageAll: true}
	if err := c.GitCommit(ctx, "/repo", commit); err != nil || b.commit != commit {
		t.Fatal(err)
	}
	b.errToReturn = engine.ErrUnknownDirectory
	if err := c.GitStage(ctx, "/unknown", nil); err == nil {
		t.Fatal("lost backend error")
	}
}
func TestGitWriteRequiresWritableCapability(t *testing.T) {
	for _, save := range []bool{false, true} {
		b := &gitWriteMock{mockBackend: &mockBackend{}}
		s, c, cleanup := setupPipePair(b)
		s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
			return b, Capabilities{SaveFile: save, GitWrite: !save}, paths.Roots{}, nil
		})
		init, err := c.Initialize(context.Background(), InitializeRequest{})
		if err != nil || init.Capabilities.GitWrite {
			t.Fatalf("cap: %+v %v", init, err)
		}
		if err := c.GitStage(context.Background(), "/repo", nil); err == nil {
			t.Fatal("unsupported client write allowed")
		}
		raw, _ := json.Marshal(GitPathsParams{Dir: "/repo"})
		if _, err := s.dispatch(context.Background(), Request{Method: "gitStage", Params: raw}); err == nil {
			t.Fatal("unadvertised raw write allowed")
		}
		if b.method != "" {
			t.Fatal("unsupported write reached backend")
		}
		cleanup()
	}
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	c := NewClient(r, rec)
	defer c.Close()
	for _, err := range []error{c.GitStage(context.Background(), "/repo", nil), c.GitUnstage(context.Background(), "/repo", nil), c.GitDiscard(context.Background(), "/repo", files.GitDiscardRequest{}), c.GitCommit(context.Background(), "/repo", files.GitCommitRequest{})} {
		if err == nil {
			t.Fatal("old backend accepted")
		}
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported request sent")
	}
}
