package app

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	scancat "github.com/ginkcode/agent-sessions/internal/scan"
)

// cdPrefix is how a copied command for this platform starts in dir.
func cdPrefix(dir string) string {
	if runtime.GOOS == "windows" {
		return "Set-Location -LiteralPath " + launch.PowerShellQuote(dir) + " -ErrorAction Stop; "
	}
	return "cd " + launch.ShellEscape(dir) + " && "
}

// captureTerminal makes a's terminal record the commands it would open.
func captureTerminal(a *App) *[]provider.Command {
	var opened []provider.Command
	a.terminalOverride = func(cmd provider.Command) error {
		opened = append(opened, cmd)
		return nil
	}
	return &opened
}

func TestLaunchInfo(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	if launch.ChooseTerminal() {
		fakeTerminals(t, a, "kitty")
	}
	info := a.LaunchInfo()
	if info.Terminal != launch.Supported() || info.ChooseTerminal != launch.ChooseTerminal() {
		t.Errorf("LaunchInfo = %+v, want terminal %v", info, launch.Supported())
	}
	want := "posix"
	if runtime.GOOS == "windows" {
		want = "powershell"
	}
	if info.Shell != want {
		t.Errorf("Shell = %q, want %q", info.Shell, want)
	}
	captureTerminal(a)
	if !a.LaunchInfo().Terminal {
		t.Error("Terminal = false with a terminal opener")
	}
}

func TestOpenInTerminalUnsupported(t *testing.T) {
	if launch.Supported() {
		t.Skip("this platform opens terminals")
	}
	a, _, _ := setupHandoffTest(t)
	err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"})
	if !errors.Is(err, launch.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestOpenResumeInTerminal(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	opened := captureTerminal(a)
	if err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}); err != nil {
		t.Fatal(err)
	}
	want := provider.Command{Argv: []string{"fake", "--resume", "sess-root"}, Dir: "/tmp/work"}
	if len(*opened) != 1 || strings.Join((*opened)[0].Argv, " ") != strings.Join(want.Argv, " ") || (*opened)[0].Dir != want.Dir {
		t.Fatalf("opened %+v, want %+v", *opened, want)
	}

	if err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "missing"}); err == nil {
		t.Error("unknown session opened")
	}
	if len(*opened) != 1 {
		t.Errorf("a failed build still opened a terminal: %+v", *opened)
	}
}

func TestOpenHandoffInTerminal(t *testing.T) {
	a, svc, _ := setupHandoffTest(t)
	opened := captureTerminal(a)
	err := a.OpenHandoffInTerminal(HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: model.AgentOpenCode,
		CWD:    "/custom/dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 1 {
		t.Fatalf("opened %d terminals", len(*opened))
	}
	cmd := (*opened)[0]
	promptFile, _ := handoff.PromptFilePath(svc.DataDir(), "sess-root")
	ctxFile, _ := handoff.ContextFilePath(svc.DataDir(), "sess-root")
	if cmd.Dir != "/custom/dir" || len(cmd.Argv) != 3 || cmd.Argv[0] != "opencode" || cmd.Argv[1] != "--prompt" ||
		!strings.Contains(cmd.Argv[2], promptFile) {
		t.Errorf("command = %+v", cmd)
	}
	// The agent must find the files the prompt names.
	for _, f := range []string{promptFile, ctxFile} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("handoff file not written: %v", err)
		}
	}
}

func TestOpenBundleHandoffInTerminal(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	svc.SetDataDir(t.TempDir())
	summary, err := svc.OpenBundleBytes(context.Background(), "test.agent-session.zip", sampleBundleBytes(t, bundle.ProfileComplete))
	if err != nil {
		t.Fatal(err)
	}
	a := NewAppWithService(svc)
	opened := captureTerminal(a)
	err = a.OpenBundleHandoffInTerminal(BundleHandoffRequest{BundleID: summary.BundleID, Target: model.AgentCodex, CWD: "/home/me/project"})
	if err != nil {
		t.Fatal(err)
	}
	promptFile, _ := handoff.PromptFilePath(svc.DataDir(), "11111111-2222-3333-4444-555555555555")
	if len(*opened) != 1 || (*opened)[0].Argv[0] != "codex" || (*opened)[0].Dir != "/home/me/project" ||
		!strings.Contains((*opened)[0].Argv[len((*opened)[0].Argv)-1], promptFile) {
		t.Fatalf("opened %+v", *opened)
	}
	if _, err := os.Stat(promptFile); err != nil {
		t.Errorf("prompt file not written: %v", err)
	}
}

func TestOpenInTerminalRefusesRemote(t *testing.T) {
	stub := &stubRemoteBackend{resumeCmd: "claude --resume s1"}
	for name, a := range map[string]*App{"connected": setupRemoteApp(stub), "offline": offlineRemoteApp()} {
		opened := captureTerminal(a)
		ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
		for op, err := range map[string]error{
			"resume":  a.OpenResumeInTerminal(ref),
			"handoff": a.OpenHandoffInTerminal(HandoffRequest{Ref: ref, Target: model.AgentCodex}),
			"bundle":  a.OpenBundleHandoffInTerminal(engine.BundleHandoffRequest{BundleID: "b-1", Target: model.AgentCodex}),
		} {
			if !errors.Is(err, ErrTerminalRemote) {
				t.Errorf("%s %s: err = %v, want ErrTerminalRemote", name, op, err)
			}
		}
		if len(*opened) != 0 {
			t.Errorf("%s: a remote session opened a local terminal: %+v", name, *opened)
		}
	}
}

// offlineRemoteApp is an App whose remote host dropped: calls must not fall
// back to Local.
func offlineRemoteApp() *App {
	a := NewApp()
	a.conn = newConnection(nil)
	a.conn.wantHost = "remote-server"
	a.conn.localActive = false
	a.conn.state = ConnectionState{Phase: ConnDisconnected, Host: "remote-server"}
	return a
}
