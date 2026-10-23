package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

type gitBranchesRecorder struct {
	engine.Backend
	lists, switches int
	req             files.GitSwitchRequest
}

func (b *gitBranchesRecorder) GitBranches(context.Context, string) (files.GitBranchList, error) {
	b.lists++
	return files.GitBranchList{Branches: []files.GitBranch{{Name: "main"}}}, nil
}
func (b *gitBranchesRecorder) GitSwitch(_ context.Context, _ string, req files.GitSwitchRequest) error {
	b.switches++
	b.req = req
	return nil
}
func TestGitBranchesHostRouting(t *testing.T) {
	local, remote := &gitBranchesRecorder{}, &gitBranchesRecorder{}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)
	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.client = remote
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 1}
	if _, err := a.GitBranches("/repo"); err != nil {
		t.Fatal(err)
	}
	req := files.GitSwitchRequest{Name: "feature", Create: true, DraftPaths: []string{"draft"}}
	if err := a.GitSwitch("/repo", req, ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatal(err)
	}
	if err := a.GitSwitch("/repo", req, "box"); err != nil {
		t.Fatal(err)
	}
	if local.lists != 0 || local.switches != 0 || remote.lists != 1 || remote.switches != 1 || len(remote.req.DraftPaths) != 1 {
		t.Fatal("wrong host routing")
	}
	a.conn.client = nil
	a.conn.state = ConnectionState{Phase: ConnReconnecting, Host: "box", Generation: 1}
	if _, err := a.GitBranches("/repo"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatal(err)
	}
	if err := a.GitSwitch("/repo", req, "box"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatal(err)
	}
	if local.lists != 0 || local.switches != 0 || remote.switches != 1 {
		t.Fatal("offline fallback")
	}
}
