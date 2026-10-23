package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

type gitWriteRecorder struct {
	engine.Backend
	calls int
}

func (b *gitWriteRecorder) GitStage(context.Context, string, []string) error   { b.calls++; return nil }
func (b *gitWriteRecorder) GitUnstage(context.Context, string, []string) error { b.calls++; return nil }
func (b *gitWriteRecorder) GitDiscard(context.Context, string, files.GitDiscardRequest) error {
	b.calls++
	return nil
}
func (b *gitWriteRecorder) GitCommit(context.Context, string, files.GitCommitRequest) error {
	b.calls++
	return nil
}
func TestGitWriteHostRouting(t *testing.T) {
	local, remote := &gitWriteRecorder{}, &gitWriteRecorder{}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)
	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.client = remote
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 1}
	for _, call := range []func(string) error{
		func(host string) error { return a.GitStage("/project", []string{"file"}, host) },
		func(host string) error { return a.GitUnstage("/project", nil, host) },
		func(host string) error { return a.GitDiscard("/project", files.GitDiscardRequest{}, host) },
		func(host string) error {
			return a.GitCommit("/project", files.GitCommitRequest{Message: "commit"}, host)
		},
	} {
		if err := call(""); !errors.Is(err, ErrSaveHostChanged) {
			t.Fatal(err)
		}
		if err := call("box"); err != nil {
			t.Fatal(err)
		}
	}
	if local.calls != 0 || remote.calls != 4 {
		t.Fatalf("routes: local %d remote %d", local.calls, remote.calls)
	}
	a.conn.client = nil
	a.conn.state = ConnectionState{Phase: ConnReconnecting, Host: "box", Generation: 1}
	if err := a.GitStage("/project", nil, "box"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatal(err)
	}
	if local.calls != 0 || remote.calls != 4 {
		t.Fatal("offline write reached backend")
	}
}
