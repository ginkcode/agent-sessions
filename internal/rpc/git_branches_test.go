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

type gitBranchesMock struct {
	*mockBackend
	dir      string
	req      files.GitSwitchRequest
	switches int
}

func (b *gitBranchesMock) GitBranches(_ context.Context, dir string) (files.GitBranchList, error) {
	b.dir = dir
	return files.GitBranchList{Branches: []files.GitBranch{{Name: "main", Current: true, Upstream: "origin/main", CommitTime: 1700000000}, {Name: "origin/next", Remote: true}}, Truncated: true}, b.errToReturn
}
func (b *gitBranchesMock) GitSwitch(_ context.Context, dir string, req files.GitSwitchRequest) error {
	b.dir = dir
	b.req = req
	b.switches++
	return b.errToReturn
}
func TestGitBranchesRPC(t *testing.T) {
	b := &gitBranchesMock{mockBackend: &mockBackend{}}
	s, c, cleanup := setupPipePair(b)
	defer cleanup()
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return b, Capabilities{GitChanges: true, GitWrite: true, SaveFile: true}, paths.Roots{}, nil
	})
	_, err := c.Initialize(context.Background(), InitializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GitBranches(context.Background(), "/repo/sub")
	want, _ := b.GitBranches(context.Background(), "/repo/sub")
	if err != nil || !reflect.DeepEqual(got, want) || b.dir != "/repo/sub" {
		t.Fatalf("list %+v %v", got, err)
	}
	req := files.GitSwitchRequest{Name: "origin/next", Remote: true, DraftPaths: []string{"draft"}}
	if err := c.GitSwitch(context.Background(), "/repo", req); err != nil || !reflect.DeepEqual(b.req, req) || b.dir != "/repo" {
		t.Fatalf("switch %v %+v", err, b.req)
	}
	b.errToReturn = engine.ErrUnknownDirectory
	if _, err := c.GitBranches(context.Background(), "/unknown"); err == nil {
		t.Fatal("lost list error")
	}
	if err := c.GitSwitch(context.Background(), "/unknown", req); err == nil {
		t.Fatal("lost switch error")
	}
}
func TestGitBranchesCapabilities(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	c := NewClient(r, rec)
	defer c.Close()
	if _, err := c.GitBranches(context.Background(), "/repo"); err == nil {
		t.Fatal("unsupported list accepted")
	}
	if err := c.GitSwitch(context.Background(), "/repo", files.GitSwitchRequest{Name: "main"}); err == nil {
		t.Fatal("unsupported switch accepted")
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported transport write")
	}
	b := &gitBranchesMock{mockBackend: &mockBackend{}}
	s, c2, cleanup := setupPipePair(b)
	defer cleanup()
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return b, Capabilities{GitChanges: true, GitWrite: true}, paths.Roots{}, nil
	})
	init, err := c2.Initialize(context.Background(), InitializeRequest{})
	if err != nil || init.Capabilities.GitWrite {
		t.Fatal("read-only advertised switch")
	}
	if _, err := c2.GitBranches(context.Background(), "/repo"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(GitSwitchParams{Dir: "/repo", Request: files.GitSwitchRequest{Name: "main"}})
	if _, err := s.dispatch(context.Background(), Request{Method: "gitSwitch", Params: raw}); err == nil {
		t.Fatal("raw read-only switch allowed")
	}
	if b.switches != 0 {
		t.Fatal("switch reached read-only backend")
	}
}
