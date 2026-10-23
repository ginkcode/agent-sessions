package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// launcherBackend is a backend that builds agent commands, as the engine does.
type launcherBackend struct {
	*mockBackend
	cmd   provider.Command
	err   error
	calls []string
}

func (b *launcherBackend) ResumeLaunch(_ context.Context, ref model.SessionRef) (provider.Command, error) {
	b.calls = append(b.calls, "resume:"+ref.ID)
	return b.cmd, b.err
}

func (b *launcherBackend) NewSessionLaunch(_ context.Context, agent model.AgentID, dir string) (provider.Command, error) {
	b.calls = append(b.calls, "new:"+string(agent)+":"+dir)
	return b.cmd, b.err
}

func (b *launcherBackend) NewSessionAgents(context.Context) ([]engine.AgentStatus, error) {
	b.calls = append(b.calls, "agents")
	return []engine.AgentStatus{{Agent: model.AgentClaude, Name: "Claude Code", Installed: true}, {Agent: model.AgentCodex, Name: "Codex", Hint: "install it"}}, b.err
}

func (b *launcherBackend) HandoffLaunch(_ context.Context, req engine.HandoffRequest) (provider.Command, error) {
	b.calls = append(b.calls, "handoff:"+string(req.Target))
	return b.cmd, b.err
}

func (b *launcherBackend) BundleHandoffLaunch(_ context.Context, req engine.BundleHandoffRequest) (provider.Command, error) {
	b.calls = append(b.calls, "bundle:"+req.BundleID)
	return b.cmd, b.err
}

func TestTerminalScriptMethods(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX terminal scripts")
	}
	t.Setenv("WSL_DISTRO_NAME", "")
	dir := filepath.Join(t.TempDir(), "it's a dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := provider.Command{Argv: []string{"sh", "--resume", "s-1"}, Dir: dir}
	want, err := launch.PosixTerminalScript(cmd)
	if err != nil {
		t.Fatal(err)
	}

	backend := &launcherBackend{mockBackend: &mockBackend{}, cmd: cmd}
	_, client, cleanup := setupPipePair(backend)
	defer cleanup()
	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}

	for name, call := range map[string]func() (string, error){
		"resume": func() (string, error) {
			return client.ResumeTerminalScript(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "s-1"})
		},
		"handoff": func() (string, error) {
			return client.HandoffTerminalScript(ctx, engine.HandoffRequest{Target: model.AgentCodex})
		},
		"bundle": func() (string, error) {
			return client.BundleHandoffTerminalScript(ctx, engine.BundleHandoffRequest{BundleID: "b-1", Target: model.AgentClaude})
		},
		"new": func() (string, error) {
			return client.NewSessionTerminalScript(ctx, model.AgentOpenCode, dir)
		},
	} {
		got, err := call()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if got != want {
			t.Errorf("%s script =\n%s\nwant\n%s", name, got, want)
		}
	}
	slices.Sort(backend.calls)
	if !slices.Equal(backend.calls, []string{"bundle:b-1", "handoff:" + string(model.AgentCodex), "new:opencode:" + dir, "resume:s-1"}) {
		t.Errorf("backend calls = %q", backend.calls)
	}

	// A panel tab's script ends with the agent.
	wantPanel, err := launch.PosixPanelScript(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if wantPanel == want {
		t.Fatal("panel and terminal scripts are the same")
	}
	for name, call := range map[string]func() (string, error){
		"resume": func() (string, error) {
			return client.ResumePanelScript(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "s-1"})
		},
		"handoff": func() (string, error) {
			return client.HandoffPanelScript(ctx, engine.HandoffRequest{Target: model.AgentCodex})
		},
		"bundle": func() (string, error) {
			return client.BundleHandoffPanelScript(ctx, engine.BundleHandoffRequest{BundleID: "b-1", Target: model.AgentClaude})
		},
		"new": func() (string, error) {
			return client.NewSessionPanelScript(ctx, model.AgentOpenCode, dir)
		},
	} {
		got, err := call()
		if err != nil {
			t.Errorf("panel %s: %v", name, err)
		} else if got != wantPanel {
			t.Errorf("panel %s script =\n%s\nwant\n%s", name, got, wantPanel)
		}
	}

	// The engine's error reaches the client as its sentinel.
	backend.err = engine.ErrUnknownSession
	if _, err := client.ResumeTerminalScript(ctx, model.SessionRef{ID: "gone"}); !errors.Is(err, engine.ErrUnknownSession) {
		t.Errorf("engine error: %v", err)
	}

	// A directory missing on the server's machine is reported, not opened.
	backend.err = nil
	backend.cmd.Dir = filepath.Join(dir, "gone")
	if _, err := client.ResumeTerminalScript(ctx, model.SessionRef{ID: "s-1"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing directory: %v", err)
	}
}

func TestTerminalScriptMethodsNeedLauncher(t *testing.T) {
	_, client, cleanup := setupPipePair(&mockBackend{})
	defer cleanup()
	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err := client.HandoffTerminalScript(ctx, engine.HandoffRequest{})
	var rerr *ResponseError
	if !errors.As(err, &rerr) || rerr.Code != CodeMethodNotFound {
		t.Errorf("backend without launcher: %v", err)
	}
}

func TestNewSessionScriptNeedsCapability(t *testing.T) {
	backend := &launcherBackend{mockBackend: &mockBackend{}}
	_, client, cleanup := setupPipePair(backend)
	defer cleanup()
	ctx := context.Background()
	res, err := client.Initialize(ctx, InitializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Capabilities.NewSession {
		t.Fatalf("capabilities = %+v", res.Capabilities)
	}
	agents, err := client.NewSessionAgents(ctx)
	if err != nil || len(agents) != 2 || !agents[0].Installed || agents[1].Installed || agents[1].Hint != "install it" {
		t.Fatalf("NewSessionAgents = %+v, %v", agents, err)
	}
	backend.calls = nil

	// An older server does not advertise it; the client asks nothing of it.
	client.caps.NewSession = false
	for _, call := range []func(context.Context, model.AgentID, string) (string, error){client.NewSessionTerminalScript, client.NewSessionPanelScript} {
		if _, err := call(ctx, model.AgentClaude, "/p"); !errors.Is(err, engine.ErrUnsupportedAction) || !strings.Contains(err.Error(), "update it") {
			t.Errorf("err = %v, want ErrUnsupportedAction", err)
		}
	}
	if _, err := client.NewSessionAgents(ctx); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Errorf("agents err = %v, want ErrUnsupportedAction", err)
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend calls = %q", backend.calls)
	}
}
