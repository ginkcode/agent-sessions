package rpc

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

type gitSyncMock struct {
	*mockBackend
	dir      string
	req      files.GitSyncRequest
	calls    int
	block    bool
	canceled chan struct{}
}

func (b *gitSyncMock) GitSync(ctx context.Context, dir string, req files.GitSyncRequest) (files.GitSyncResult, error) {
	b.dir, b.req = dir, req
	b.calls++
	if b.block {
		<-ctx.Done()
		close(b.canceled)
		return files.GitSyncResult{}, ctx.Err()
	}
	return files.GitSyncResult{Op: req.Op, Summary: "Fetched origin.", Status: files.RepoStatus{OK: true, Branch: "main", Upstream: "origin/main", Behind: 2, Entries: []files.GitChange{}}}, b.errToReturn
}

func startGitSync(t *testing.T, b *gitSyncMock, caps Capabilities) (*Client, func()) {
	t.Helper()
	s, c, cleanup := setupPipePair(b)
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return b, caps, paths.Roots{}, nil
	})
	if _, err := c.Initialize(context.Background(), InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	return c, cleanup
}

func TestGitSyncRPC(t *testing.T) {
	b := &gitSyncMock{mockBackend: &mockBackend{}}
	c, cleanup := startGitSync(t, b, Capabilities{GitChanges: true, GitWrite: true, GitSync: true, SaveFile: true})
	defer cleanup()
	got, err := c.GitSync(context.Background(), "/repo/sub", files.GitSyncRequest{Op: files.GitSyncFetch})
	want, _ := b.GitSync(context.Background(), "/repo/sub", files.GitSyncRequest{Op: files.GitSyncFetch})
	if err != nil || !reflect.DeepEqual(got, want) || b.dir != "/repo/sub" || b.req.Op != files.GitSyncFetch {
		t.Fatalf("sync %+v %v", got, err)
	}
	b.errToReturn = files.NewGitSyncError(files.GitSyncNonFastForward, "diverged\n! [rejected] main -> main (fetch first)")
	_, err = c.GitSync(context.Background(), "/repo", files.GitSyncRequest{Op: files.GitSyncPush})
	var syncErr *files.GitSyncError
	if !errors.As(err, &syncErr) || syncErr.Code != files.GitSyncNonFastForward || err.Error() != b.errToReturn.Error() {
		t.Fatalf("code lost over RPC: %v", err)
	}
	b.errToReturn = engine.ErrUnknownDirectory
	if _, err := c.GitSync(context.Background(), "/unknown", files.GitSyncRequest{Op: files.GitSyncPull}); err == nil {
		t.Fatal("lost error")
	}
}

func TestGitSyncRPCCancellationReachesBackend(t *testing.T) {
	b := &gitSyncMock{mockBackend: &mockBackend{}, block: true, canceled: make(chan struct{})}
	c, cleanup := startGitSync(t, b, Capabilities{GitWrite: true, GitSync: true, SaveFile: true})
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := c.GitSync(ctx, "/repo", files.GitSyncRequest{Op: files.GitSyncPush}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-b.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("server request context was not canceled")
	}
}

func TestGitSyncCapabilities(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	c := NewClient(r, rec)
	defer c.Close()
	if _, err := c.GitSync(context.Background(), "/repo", files.GitSyncRequest{Op: files.GitSyncFetch}); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported transport write")
	}
	// Without writable Git the server neither advertises nor dispatches sync.
	b := &gitSyncMock{mockBackend: &mockBackend{}}
	c2, cleanup := startGitSync(t, b, Capabilities{GitWrite: true, GitSync: true})
	defer cleanup()
	if c2.caps.GitSync {
		t.Fatal("sync advertised without SaveFile/GitWrite")
	}
	c2.caps.GitSync = true
	if _, err := c2.GitSync(context.Background(), "/repo", files.GitSyncRequest{Op: files.GitSyncPush}); !errors.Is(err, engine.ErrUnsupportedAction) || b.calls != 0 {
		t.Fatalf("server dispatched disabled sync: %v", err)
	}
}
