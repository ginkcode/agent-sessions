package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// replaceRecorder is a backend that records the replaces that reach it.
type replaceRecorder struct {
	engine.Backend
	mu   sync.Mutex
	dirs []string
}

func (b *replaceRecorder) ReplaceInFiles(_ context.Context, dir string, req files.ReplaceRequest) (files.ReplaceResult, error) {
	b.mu.Lock()
	b.dirs = append(b.dirs, dir)
	b.mu.Unlock()
	return files.ReplaceResult{Replaced: len(req.Files)}, nil
}

func (b *replaceRecorder) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.dirs...)
}

var testReplace = files.ReplaceRequest{Query: files.SearchQuery{Pattern: "x"}, Files: []files.ReplaceFile{{Path: "f.txt"}}}

func TestApp_ReplaceInFilesRoutesToSearchedHost(t *testing.T) {
	local, remoteBox := &replaceRecorder{}, &replaceRecorder{}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)

	if _, err := a.ReplaceInFiles("/project", testReplace, "box"); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("replace for box on Local err = %v, want ErrSaveHostChanged", err)
	}
	if res, err := a.ReplaceInFiles("/project", testReplace, ""); err != nil || res.Replaced != 1 {
		t.Fatalf("local replace = %+v, %v", res, err)
	}

	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.client = remoteBox
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 1, Capabilities: rpc.Capabilities{ReplaceFiles: true}}
	if _, err := a.ReplaceInFiles("/remote", testReplace, ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("replace for Local on box err = %v, want ErrSaveHostChanged", err)
	}
	if res, err := a.ReplaceInFiles("/remote", testReplace, "box"); err != nil || res.Replaced != 1 {
		t.Fatalf("remote replace = %+v, %v", res, err)
	}

	// The link drops: nothing falls back to Local.
	a.conn.client = nil
	a.conn.state = ConnectionState{Phase: ConnReconnecting, Host: "box", Generation: 1, Error: "connection lost"}
	if _, err := a.ReplaceInFiles("/remote", testReplace, "box"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("offline replace err = %v, want ErrDisconnected", err)
	}
	if got := local.calls(); len(got) != 1 || got[0] != "/project" {
		t.Fatalf("local replaces = %v", got)
	}
	if got := remoteBox.calls(); len(got) != 1 || got[0] != "/remote" {
		t.Fatalf("remote replaces = %v", got)
	}
}
