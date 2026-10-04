package app

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// wslStub is a connected distro's server: it builds terminal scripts.
type wslStub struct {
	*stubRemoteBackend
	err   error
	calls []string
}

func (s *wslStub) ResumeTerminalScript(_ context.Context, ref model.SessionRef) (string, error) {
	s.calls = append(s.calls, "resume:"+ref.ID)
	return "script resume " + ref.ID, s.err
}

func (s *wslStub) HandoffTerminalScript(_ context.Context, req engine.HandoffRequest) (string, error) {
	s.calls = append(s.calls, "handoff:"+req.Ref.ID)
	return "script handoff " + string(req.Target), s.err
}

func (s *wslStub) BundleHandoffTerminalScript(_ context.Context, req engine.BundleHandoffRequest) (string, error) {
	s.calls = append(s.calls, "bundle:"+req.BundleID)
	return "script bundle " + string(req.Target), s.err
}

// wslApp is an App connected to the Ubuntu distro, recording the consoles it
// would open and any local terminal it should never open.
func wslApp(backend engine.Backend) (*App, *[][2]string, *int) {
	a := NewApp()
	a.conn = newConnection(nil)
	a.conn.wantHost = "wsl:Ubuntu"
	a.conn.localActive = false
	a.conn.client = backend
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: "wsl:Ubuntu", Generation: 1}
	var opened [][2]string
	a.wslTerminalOverride = func(distro, script string) error {
		opened = append(opened, [2]string{distro, script})
		return nil
	}
	local := 0
	a.terminalOverride = func(cmd provider.Command) error {
		local++
		return nil
	}
	return a, &opened, &local
}

func TestOpenInTerminalWSL(t *testing.T) {
	stub := &wslStub{stubRemoteBackend: &stubRemoteBackend{}}
	a, opened, local := wslApp(stub)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	for _, err := range []error{
		a.OpenResumeInTerminal(ref),
		a.OpenHandoffInTerminal(HandoffRequest{Ref: ref, Target: model.AgentCodex}),
		a.OpenBundleHandoffInTerminal(engine.BundleHandoffRequest{BundleID: "b-1", Target: model.AgentOpenCode}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := [][2]string{
		{"Ubuntu", "script resume s1"},
		{"Ubuntu", "script handoff " + string(model.AgentCodex)},
		{"Ubuntu", "script bundle " + string(model.AgentOpenCode)},
	}
	if !slices.Equal(*opened, want) {
		t.Errorf("opened %q, want %q", *opened, want)
	}
	if !slices.Equal(stub.calls, []string{"resume:s1", "handoff:s1", "bundle:b-1"}) {
		t.Errorf("server calls = %q", stub.calls)
	}
	if *local != 0 {
		t.Errorf("a WSL session opened %d local terminals", *local)
	}
	if !a.LaunchInfo().WSL {
		t.Error("LaunchInfo.WSL = false with a WSL opener")
	}

	// A script the distro could not build opens nothing.
	stub.err = errors.New("codex was not found")
	if err := a.OpenResumeInTerminal(ref); err == nil || err.Error() != "codex was not found" {
		t.Errorf("server error: %v", err)
	}
	if len(*opened) != 3 {
		t.Errorf("a failed script opened a console: %q", *opened)
	}
}

func TestOpenInTerminalWSLOffline(t *testing.T) {
	a, opened, local := wslApp(nil)
	a.conn.state = ConnectionState{Phase: ConnDisconnected, Host: "wsl:Ubuntu"}
	err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "s1"})
	if !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("offline: %v, want ErrDisconnected", err)
	}
	if len(*opened) != 0 || *local != 0 {
		t.Errorf("offline opened %q and %d local terminals", *opened, *local)
	}
}

func TestOpenInTerminalWSLWithoutWSL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this machine may have WSL")
	}
	a, _, local := wslApp(&wslStub{stubRemoteBackend: &stubRemoteBackend{}})
	a.wslTerminalOverride = nil
	if err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); !errors.Is(err, launch.ErrWSLNotInstalled) {
		t.Errorf("without WSL: %v", err)
	}
	if a.LaunchInfo().WSL {
		t.Error("LaunchInfo.WSL = true without WSL")
	}
	if *local != 0 {
		t.Errorf("opened %d local terminals", *local)
	}
}

func TestListWSLDistrosWithoutWSL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this machine may have WSL")
	}
	got, err := NewApp().ListWSLDistros()
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListWSLDistros = %v, %v; want an empty list", got, err)
	}
}
