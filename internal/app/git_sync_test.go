package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

type gitSyncRecorder struct {
	engine.Backend
	calls   int
	req     files.GitSyncRequest
	started chan struct{}
}

func (b *gitSyncRecorder) GitSync(ctx context.Context, _ string, req files.GitSyncRequest) (files.GitSyncResult, error) {
	b.calls++
	b.req = req
	if b.started != nil {
		close(b.started)
		<-ctx.Done()
		return files.GitSyncResult{}, ctx.Err() // like rpc.Client after it sends the cancel
	}
	return files.GitSyncResult{Op: req.Op, Summary: "Pushed main to origin/main."}, nil
}

func remoteApp(local, remote engine.Backend) *App {
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)
	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.client = remote
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 1}
	return a
}

func TestGitSyncHostRouting(t *testing.T) {
	local, remote := &gitSyncRecorder{}, &gitSyncRecorder{}
	a := remoteApp(local, remote)
	req := files.GitSyncRequest{Op: files.GitSyncPush}
	if _, err := a.GitSync("/repo", req, "s1", ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatal(err)
	}
	if got, err := a.GitSync("/repo", req, "s1", "box"); err != nil || got.Summary == "" {
		t.Fatal(got, err)
	}
	if local.calls != 0 || remote.calls != 1 || remote.req != req {
		t.Fatal("wrong host routing")
	}
	a.conn.client = nil
	a.conn.state = ConnectionState{Phase: ConnReconnecting, Host: "box", Generation: 1}
	if _, err := a.GitSync("/repo", req, "s1", "box"); !errors.Is(err, rpc.ErrDisconnected) || local.calls != 0 {
		t.Fatal("offline fallback", err)
	}
	old := remoteApp(local, &struct{ engine.Backend }{})
	if _, err := old.GitSync("/repo", req, "s1", "box"); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Fatal(err)
	}
}

func TestGitSyncCancelByID(t *testing.T) {
	remote := &gitSyncRecorder{started: make(chan struct{})}
	a := remoteApp(&gitSyncRecorder{}, remote)
	errs := make(chan error, 1)
	go func() {
		_, err := a.GitSync("/repo", files.GitSyncRequest{Op: files.GitSyncPull}, "sync-1", "box")
		errs <- err
	}()
	<-remote.started
	a.CancelGitSync("other")
	a.CancelGitSync("sync-1")
	select {
	case err := <-errs:
		var syncErr *files.GitSyncError
		if !errors.As(err, &syncErr) || syncErr.Code != files.GitSyncCanceled || !strings.HasPrefix(err.Error(), "canceled: ") {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the sync")
	}
	a.CancelGitSync("sync-1") // finished: no-op
}
