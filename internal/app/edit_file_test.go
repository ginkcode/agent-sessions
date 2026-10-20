package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// saveRecorder is a backend that records the saves that reach it.
type saveRecorder struct {
	engine.Backend
	name  string
	mu    sync.Mutex
	saves []string
}

func (b *saveRecorder) SaveFile(_ context.Context, dir string, req files.SaveRequest) (files.SaveResult, error) {
	b.mu.Lock()
	b.saves = append(b.saves, dir+"|"+req.Path)
	b.mu.Unlock()
	return files.SaveResult{Status: files.SaveStatusSaved, Content: &files.Content{Path: req.Path, Text: b.name}}, nil
}

func (b *saveRecorder) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.saves...)
}

var testSave = files.SaveRequest{Path: "f.txt", Text: "y\n", ExpectedRevision: "sha256:" + strings.Repeat("0", 64)}

func TestApp_SaveFileOfflineNeverReachesLocal(t *testing.T) {
	local := &saveRecorder{name: "local"}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)
	// Was connected to box, then the link dropped.
	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.state = ConnectionState{Phase: ConnReconnecting, Host: "box", Generation: 1, Error: "connection lost"}

	res, err := a.SaveFile("/project", testSave, "box")
	if !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("SaveFile err = %v, want ErrDisconnected", err)
	}
	if res.Status != "" || res.Content != nil {
		t.Fatalf("offline result = %+v", res)
	}
	if got := local.calls(); len(got) != 0 {
		t.Fatalf("save reached Local while disconnected from box: %v", got)
	}

	// Disconnect is the only way back to Local.
	a.conn.Disconnect()
	res, err = a.SaveFile("/project", testSave, "")
	if err != nil || res.Content == nil || res.Content.Text != "local" {
		t.Fatalf("after disconnect = %+v, %v", res, err)
	}
	if got := local.calls(); len(got) != 1 || got[0] != "/project|f.txt" {
		t.Fatalf("local saves = %v", got)
	}
}

func TestApp_SaveFileRoutesToSelectedHost(t *testing.T) {
	local := &saveRecorder{name: "local"}
	remoteBox := &saveRecorder{name: "box"}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)
	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.client = remoteBox
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 1, Capabilities: rpc.Capabilities{SaveFile: true}}

	res, err := a.SaveFile("/remote/project", testSave, "box")
	if err != nil || res.Content == nil || res.Content.Text != "box" {
		t.Fatalf("connected save = %+v, %v", res, err)
	}
	if got := remoteBox.calls(); len(got) != 1 || got[0] != "/remote/project|f.txt" {
		t.Fatalf("remote saves = %v", got)
	}
	if got := local.calls(); len(got) != 0 {
		t.Fatalf("save of a remote host's path reached Local: %v", got)
	}

	// Switching to another host that is still connecting fails rather
	// than saving on the previous host or Local.
	a.conn.mu.Lock()
	a.conn.wantHost = "other"
	a.conn.client = nil
	a.conn.state = ConnectionState{Phase: ConnConnecting, Host: "other", Generation: 2}
	a.conn.mu.Unlock()
	if _, err := a.SaveFile("/remote/project", testSave, "box"); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("save meant for box err = %v, want ErrSaveHostChanged", err)
	}
	if _, err := a.SaveFile("/remote/project", testSave, "other"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("connecting save err = %v, want ErrDisconnected", err)
	}
	if got := remoteBox.calls(); len(got) != 1 {
		t.Fatalf("save reached previous host: %v", got)
	}
	if got := local.calls(); len(got) != 0 {
		t.Fatalf("save reached Local: %v", got)
	}
}

func TestApp_SaveFileRefusesAnotherHost(t *testing.T) {
	local := &saveRecorder{name: "local"}
	remoteBox := &saveRecorder{name: "box"}
	a := NewApp()
	a.backend = local
	a.conn = newConnection(nil)

	// Local serves; a save the editor meant for box must not land here.
	if _, err := a.SaveFile("/project", testSave, "box"); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("save for box on Local err = %v, want ErrSaveHostChanged", err)
	}

	a.conn.wantHost = "box"
	a.conn.localActive = false
	a.conn.client = remoteBox
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "box", Generation: 1, Capabilities: rpc.Capabilities{SaveFile: true}}
	for _, host := range []string{"", "other"} {
		if _, err := a.SaveFile("/project", testSave, host); !errors.Is(err, ErrSaveHostChanged) {
			t.Fatalf("save for %q on box err = %v, want ErrSaveHostChanged", host, err)
		}
	}
	if got := append(local.calls(), remoteBox.calls()...); len(got) != 0 {
		t.Fatalf("a save for another host reached a backend: %v", got)
	}
}

func TestOfflineBackend_SaveFile(t *testing.T) {
	res, err := offlineBackend{host: "box"}.SaveFile(context.Background(), "/p", testSave)
	if !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("err = %v, want ErrDisconnected", err)
	}
	if res.Status != "" || res.Content != nil || res.ObservedRevision != "" {
		t.Fatalf("result = %+v", res)
	}
}
