package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/remote"
)

func forwardApp(t *testing.T) *App {
	t.Helper()
	a := &App{}
	c := newConnection(nil)
	c.gen = 1
	c.state = ConnectionState{Phase: ConnConnected, Host: "server", Generation: 1}
	c.forwards = newPortForwards(filepath.Join(t.TempDir(), "port-forwards.json"))
	c.forwards.sync(c.state, c.opts)
	a.conn = c
	t.Cleanup(func() { c.Shutdown() })
	return a
}
func TestPortForwardPersistenceAndIsolation(t *testing.T) {
	a := forwardApp(t)
	s, err := a.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 80, LocalPort: 18080, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Host != "server" || len(s.Rules) != 1 || s.Rules[0].Status != "stopped" || s.Rules[0].ID == "" {
		t.Fatal(s)
	}
	id := s.Rules[0].ID
	loaded := newPortForwards(a.conn.forwards.path)
	if loaded.loadErr != nil || loaded.saved["server"][0].ID != id {
		t.Fatal("not persisted", loaded)
	}
	a.conn.mu.Lock()
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "other", Generation: 2}
	a.conn.mu.Unlock()
	s, err = a.ListPortForwards()
	if err != nil || len(s.Rules) != 0 {
		t.Fatal(s, err)
	}
	if _, err = a.RemovePortForward(id); err == nil {
		t.Fatal("removed another host's rule")
	}
	a.conn.mu.Lock()
	a.conn.state = ConnectionState{Phase: ConnDisconnected, Host: "server", Generation: 3}
	a.conn.mu.Unlock()
	if _, err = a.StartPortForward(id); err == nil {
		t.Fatal("started while disconnected")
	}
	if _, err = a.RemovePortForward(id); err != nil {
		t.Fatal(err)
	}
	if len(newPortForwards(a.conn.forwards.path).saved["server"]) != 0 {
		t.Fatal("remove not persisted")
	}
}
func TestPortForwardValidationAndEdit(t *testing.T) {
	a := forwardApp(t)
	if _, err := a.SavePortForward(PortForwardRequest{RemoteHost: "bad;host", RemotePort: 80, LocalPort: 8080}); err == nil {
		t.Fatal("invalid hostname accepted")
	}
	s, err := a.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 80, LocalPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 90, LocalPort: 8080}); err == nil {
		t.Fatal("duplicate port accepted")
	}
	r := s.Rules[0].PortForwardRequest
	r.RemotePort = 90
	if _, err = a.SavePortForward(r); err != nil {
		t.Fatal(err)
	}
	if _, err = a.StopPortForward(r.ID); err != nil {
		t.Fatal(err)
	}
	a.conn.Disconnect()
	s, err = a.ListPortForwards()
	if err != nil || s.Host != "" || len(s.Rules) != 0 {
		t.Fatal(s, err)
	}
}
func TestForwardLogReadyAndFailure(t *testing.T) {
	ready := false
	l := &forwardLog{ready: func() { ready = true }}
	l.Write([]byte("debug1: Local forwarding listening on 127.0.0.1 port 8080.\ndebug1: Entering interactive "))
	if ready {
		t.Fatal("partial line ready")
	}
	l.Write([]byte("session.\nbind: Address already in use\n"))
	if !ready || l.failure() != "bind: Address already in use" {
		t.Fatal(ready, l.failure())
	}
}
func TestPortForwardAsyncFailureAndDisconnect(t *testing.T) {
	a := forwardApp(t)
	a.conn.opts.Binary = filepath.Join(t.TempDir(), "missing-ssh")
	s, err := a.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 80, LocalPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	id := s.Rules[0].ID
	s, err = a.StartPortForward(id)
	if err != nil || s.Rules[0].Status != "starting" {
		t.Fatal(s, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ = a.ListPortForwards()
		if s.Rules[0].Status == "failed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if s.Rules[0].Status != "failed" || s.Rules[0].Error == "" {
		t.Fatal(s)
	}
	// Cancellation invalidates the run before its completion can overwrite state.
	p := a.conn.forwards
	p.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	p.runs[id] = &forwardRun{cancel: cancel}
	p.states[id] = PortForwardRule{PortForwardRequest: s.Rules[0].PortForwardRequest, Status: "active"}
	p.mu.Unlock()
	a.conn.setPhase(1, ConnDisconnected, "server", "lost", a.conn.state.Capabilities, "")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not cancelled")
	}
	s, _ = a.ListPortForwards()
	if s.Rules[0].Status != "disconnected" {
		t.Fatal(s)
	}
}
func TestPortForwardCorruptPersistenceNotOverwritten(t *testing.T) {
	a := forwardApp(t)
	path := a.conn.forwards.path
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	a.conn.forwards = newPortForwards(path)
	if _, err := a.ListPortForwards(); err == nil {
		t.Fatal("ignored corrupt persistence")
	}
}

// Two windows share the rules file: edits made in one are seen by the
// other, and neither overwrites the other's rules.
func TestPortForwardEditsFromTwoWindows(t *testing.T) {
	a := forwardApp(t)
	b := forwardApp(t)
	b.conn.forwards = newPortForwards(a.conn.forwards.path)
	b.conn.forwards.sync(b.conn.state, b.conn.opts)

	if _, err := a.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 80, LocalPort: 18080}); err != nil {
		t.Fatal(err)
	}
	// b read the file before a's rule existed.
	s, err := b.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 90, LocalPort: 18090})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rules) != 2 {
		t.Fatalf("b sees %d rules, want both windows' rules: %+v", len(s.Rules), s.Rules)
	}
	if _, err := b.SavePortForward(PortForwardRequest{RemoteHost: "localhost", RemotePort: 91, LocalPort: 18080}); err == nil {
		t.Fatal("b reused a's local port")
	}
	s, err = a.ListPortForwards()
	if err != nil || len(s.Rules) != 2 {
		t.Fatalf("a sees %+v, %v", s.Rules, err)
	}
	if _, err := a.RemovePortForward(s.Rules[1].ID); err != nil {
		t.Fatalf("a removes b's rule: %v", err)
	}
	if got := newPortForwards(a.conn.forwards.path).saved["server"]; len(got) != 1 || got[0].LocalPort != 18080 {
		t.Fatalf("saved %+v", got)
	}
}

func TestPortForwardAutoStart(t *testing.T) {
	p := newPortForwards(filepath.Join(t.TempDir(), "rules.json"))
	p.mu.Lock()
	err := p.persistLocked(map[string][]PortForwardRequest{"server": {{ID: "auto", RemoteHost: "localhost", RemotePort: 80, LocalPort: 8080, AutoStart: true}, {ID: "manual", RemoteHost: "localhost", RemotePort: 90, LocalPort: 8090}}})
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p.sync(ConnectionState{Phase: ConnConnected, Host: "server", Generation: 1}, remote.SSHOptions{Binary: "/nonexistent/ssh"})
	p.mu.Lock()
	if p.runs["auto"] == nil || p.runs["manual"] != nil {
		t.Fatal("wrong autostart rules")
	}
	p.mu.Unlock()
	p.sync(ConnectionState{Phase: ConnLocal}, remote.SSHOptions{})
}
