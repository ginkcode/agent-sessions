package app

import (
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/scan"
	"github.com/ginkcode/agent-sessions/internal/term"
)

// newSessionApp is a local App whose catalog knows one project, an existing
// directory, through a session of the fake Claude Code provider.
func newSessionApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	catalog := scan.NewCatalog()
	now := time.Now()
	catalog.Apply(provider.ScanResult{Changed: []model.SessionMeta{{
		Ref: model.SessionRef{Agent: model.AgentClaude, ID: "s1"}, CWD: dir, CreatedAt: now, UpdatedAt: now,
	}}})
	svc := NewService(catalog, provider.Set{fake})
	svc.SetDataDir(t.TempDir())
	return NewAppWithService(svc), dir
}

func TestNewSessionLocal(t *testing.T) {
	a, dir := newSessionApp(t)
	f := withPanel(t, a)
	tab, err := a.PanelNewSession(model.AgentClaude, dir, TerminalSize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Label != "Claude Code" || tab.Kind != term.KindAgent || tab.Agent != string(model.AgentClaude) || tab.Dir != dir || tab.Host != "" {
		t.Errorf("tab = %+v", tab)
	}
	if spec := f.started[0]; !slices.Equal(spec.Argv, []string{"run", "fake"}) || spec.Dir != dir || spec.Cols != 100 {
		t.Errorf("spec = %+v", spec)
	}

	agents, err := a.NewSessionAgents()
	if err != nil || len(agents) != 1 || agents[0].Agent != model.AgentClaude || agents[0].Name != "Claude Code" {
		t.Errorf("NewSessionAgents = %+v, %v", agents, err)
	}

	opened := captureTerminal(a)
	if err := a.OpenNewSessionInTerminal(model.AgentClaude, dir); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 1 || !slices.Equal((*opened)[0].Argv, []string{"fake"}) || (*opened)[0].Dir != dir {
		t.Errorf("external terminal opened %+v", *opened)
	}
}

func TestNewSessionRefusals(t *testing.T) {
	a, dir := newSessionApp(t)
	f := withPanel(t, a)
	opened := captureTerminal(a)
	for _, tc := range []struct {
		name  string
		agent model.AgentID
		dir   string
		want  error
	}{
		{"unknown directory", model.AgentClaude, t.TempDir(), engine.ErrUnknownDirectory},
		{"no directory", model.AgentClaude, "", engine.ErrUnknownDirectory},
		{"agent without a provider", model.AgentCodex, dir, engine.ErrUnknownProvider},
	} {
		if _, err := a.PanelNewSession(tc.agent, tc.dir, TerminalSize{}); !errors.Is(err, tc.want) {
			t.Errorf("%s: panel err = %v, want %v", tc.name, err, tc.want)
		}
		if err := a.OpenNewSessionInTerminal(tc.agent, tc.dir); !errors.Is(err, tc.want) {
			t.Errorf("%s: external err = %v, want %v", tc.name, err, tc.want)
		}
	}

	a.panelOverride.agentArgv = func(provider.Command) ([]string, error) { return nil, launch.ErrAgentNotFound }
	if _, err := a.PanelNewSession(model.AgentClaude, dir, TerminalSize{}); !errors.Is(err, launch.ErrAgentNotFound) {
		t.Errorf("missing agent: %v", err)
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PanelNewSession(model.AgentClaude, dir, TerminalSize{}); err == nil {
		t.Error("a session started in a directory that is gone")
	}
	if len(f.started) != 0 || len(*opened) != 0 {
		t.Errorf("started %+v and opened %+v", f.started, *opened)
	}
}

func TestNewSessionWSL(t *testing.T) {
	stub := &wslStub{stubRemoteBackend: &stubRemoteBackend{}}
	a, opened, local := wslApp(stub)
	f := withPanel(t, a)
	tab, err := a.PanelNewSession(model.AgentCodex, "/home/me/p", TerminalSize{})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Host != "wsl:Ubuntu" || tab.Dir != "/home/me/p" || tab.Label != "Codex" || tab.Agent != string(model.AgentCodex) {
		t.Errorf("tab = %+v", tab)
	}
	if want := append([]string{`C:\wsl.exe`}, launch.WSLExecArgs("Ubuntu", "panel script new codex")...); !slices.Equal(f.started[0].Argv, want) {
		t.Errorf("argv = %q, want %q", f.started[0].Argv, want)
	}
	if err := a.OpenNewSessionInTerminal(model.AgentOpenCode, "/home/me/p"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*opened, [][2]string{{"Ubuntu", "script new opencode"}}) || *local != 0 {
		t.Errorf("opened %q, %d local", *opened, *local)
	}
	if !slices.Equal(stub.calls, []string{"panel new:codex:/home/me/p", "new:opencode:/home/me/p"}) {
		t.Errorf("server calls = %q", stub.calls)
	}
}

func TestNewSessionSSH(t *testing.T) {
	a, stub, f := sshPanelApp(t, "remote-server")
	tab, err := a.PanelNewSession(model.AgentOpenCode, "/srv/app", TerminalSize{})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Host != "remote-server" || tab.Dir != "/srv/app" || tab.Label != "OpenCode" {
		t.Errorf("tab = %+v", tab)
	}
	argv := f.started[0].Argv
	if got, want := argv[len(argv)-1], remote.QuoteArgs(launch.PosixBootstrapArgv("panel script new opencode")); got != want {
		t.Errorf("remote command = %q, want %q", got, want)
	}
	if err := a.OpenNewSessionInTerminal(model.AgentOpenCode, "/srv/app"); !errors.Is(err, ErrTerminalRemote) {
		t.Errorf("external: %v", err)
	}
	if agents, err := a.NewSessionAgents(); err != nil || len(agents) != 1 || !agents[0].Installed {
		t.Errorf("NewSessionAgents = %+v, %v", agents, err)
	}
	if !slices.Equal(stub.calls, []string{"panel new:opencode:/srv/app", "agents"}) {
		t.Errorf("server calls = %q", stub.calls)
	}
}

func TestNewSessionOffline(t *testing.T) {
	a := offlineRemoteApp()
	withPanel(t, a)
	if _, err := a.PanelNewSession(model.AgentClaude, "/srv/app", TerminalSize{}); !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("panel: %v", err)
	}
	if _, err := a.NewSessionAgents(); !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("agents: %v", err)
	}
}
