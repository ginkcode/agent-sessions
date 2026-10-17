package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/term"
)

// fakeTerminals records what the panel starts and is sent.
type fakeTerminalManager struct {
	started  []term.Spec
	tabs     []term.Tab
	startErr error
	writes   []string
	resizes  [][2]int
	acks     []int
	closed   []string
	running  map[string]bool
	closeAll int
}

func (f *fakeTerminalManager) Start(spec term.Spec, info term.Tab) (term.Tab, error) {
	if f.startErr != nil {
		return term.Tab{}, f.startErr
	}
	info.ID = "t" + string(rune('1'+len(f.started)))
	f.started = append(f.started, spec)
	f.tabs = append(f.tabs, info)
	return info, nil
}

func (f *fakeTerminalManager) Write(id string, data []byte) error {
	f.writes = append(f.writes, id+":"+string(data))
	return nil
}

func (f *fakeTerminalManager) Resize(_ string, cols, rows int) error {
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func (f *fakeTerminalManager) Ack(_ string, n int)      { f.acks = append(f.acks, n) }
func (f *fakeTerminalManager) Close(id string) error    { f.closed = append(f.closed, id); return nil }
func (f *fakeTerminalManager) CloseAll()                { f.closeAll++ }
func (f *fakeTerminalManager) Running(host string) bool { return f.running[host] }

func (f *fakeTerminalManager) Tab(id string) (term.Tab, error) {
	for _, tab := range f.tabs {
		if tab.ID == id && !slices.Contains(f.closed, id) {
			return tab, nil
		}
	}
	return term.Tab{}, term.ErrNotFound
}

// withPanel gives a a fake panel on a fake computer, where the local agent
// argv is the command's own and WSL is at C:\wsl.exe.
func withPanel(t *testing.T, a *App) *fakeTerminalManager {
	t.Helper()
	f := &fakeTerminalManager{running: map[string]bool{}}
	a.terminals = f
	a.roots.Config = t.TempDir()
	home := t.TempDir()
	a.panelOverride = &panelSystem{
		agentArgv: func(cmd provider.Command) ([]string, error) { return append([]string{"run"}, cmd.Argv...), nil },
		shellArgv: func() ([]string, error) { return []string{"/bin/fake-shell", "-l"}, nil },
		wslBinary: func() (string, error) { return `C:\wsl.exe`, nil },
		env:       func() []string { return []string{"LOGIN=1"} },
		home:      func() (string, error) { return home, nil },
		dropFiles: func(_ context.Context, host string, _ remote.SSHOptions, paths []string) ([]string, error) {
			out := make([]string, len(paths))
			for i, p := range paths {
				out[i] = "/drop/" + host + "/" + filepath.Base(p)
			}
			return out, nil
		},
	}
	if a.conn != nil {
		a.conn.opts.ControlPath = filepath.Join(t.TempDir(), "cm-%C")
	}
	return f
}

func TestPanelUnavailable(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	a.roots.Config = t.TempDir()
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}
	if _, err := a.PanelResume(ref, TerminalSize{}); !errors.Is(err, ErrPanelUnavailable) {
		t.Errorf("PanelResume = %v", err)
	}
	if _, err := a.PanelShell(PanelShellRequest{}, TerminalSize{}); !errors.Is(err, ErrPanelUnavailable) {
		t.Errorf("PanelShell = %v", err)
	}
	if err := a.TerminalWrite("t1", "x"); !errors.Is(err, ErrPanelUnavailable) {
		t.Errorf("TerminalWrite = %v", err)
	}
	a.TerminalAck("t1", 1)
	if err := a.TerminalClose("t1"); err != nil {
		t.Errorf("TerminalClose = %v", err)
	}
	if info := a.LaunchInfo(); info.Panel || info.Open != OpenInExternal {
		t.Errorf("LaunchInfo = %+v, want no panel and external", info)
	}
}

func TestPanelLocal(t *testing.T) {
	a, svc, _ := setupHandoffTest(t)
	f := withPanel(t, a)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}
	tab, err := a.PanelResume(ref, TerminalSize{Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	if tab.ID == "" || tab.Host != "" || tab.Kind != term.KindAgent || tab.Dir != "/tmp/work" {
		t.Errorf("tab = %+v", tab)
	}
	spec := f.started[0]
	if !slices.Equal(spec.Argv, []string{"run", "fake", "--resume", "sess-root"}) || spec.Dir != "/tmp/work" ||
		!slices.Equal(spec.Env, []string{"LOGIN=1"}) || spec.Cols != 120 || spec.Rows != 40 {
		t.Errorf("spec = %+v", spec)
	}

	if _, err := a.PanelHandoff(HandoffRequest{Ref: ref, Target: model.AgentOpenCode, CWD: "/custom/dir"}, TerminalSize{}); err != nil {
		t.Fatal(err)
	}
	if spec := f.started[1]; spec.Argv[1] != "opencode" || spec.Dir != "/custom/dir" {
		t.Errorf("handoff spec = %+v", spec)
	}
	if p, _ := handoff.PromptFilePath(svc.DataDir(), "sess-root"); !fileExists(p) {
		t.Error("handoff files not written")
	}

	if info := a.LaunchInfo(); !info.Panel || info.Open != OpenInPanel {
		t.Errorf("LaunchInfo = %+v, want the panel by default", info)
	}
}

func TestPanelLocalErrors(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	f := withPanel(t, a)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}
	f.startErr = errors.New("no pty")
	if _, err := a.PanelResume(ref, TerminalSize{}); err == nil || err.Error() != "no pty" {
		t.Errorf("start failure: %v", err)
	}
	f.startErr = nil
	a.panelOverride.agentArgv = func(provider.Command) ([]string, error) { return nil, launch.ErrAgentNotFound }
	if _, err := a.PanelResume(ref, TerminalSize{}); !errors.Is(err, launch.ErrAgentNotFound) {
		t.Errorf("missing agent: %v", err)
	}
	if len(f.started) != 0 {
		t.Errorf("started %+v", f.started)
	}
}

func TestPanelWSL(t *testing.T) {
	stub := &wslStub{stubRemoteBackend: &stubRemoteBackend{}}
	a, opened, local := wslApp(stub)
	f := withPanel(t, a)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	for _, start := range []func() (term.Tab, error){
		func() (term.Tab, error) { return a.PanelResume(ref, TerminalSize{}) },
		func() (term.Tab, error) {
			return a.PanelHandoff(HandoffRequest{Ref: ref, Target: model.AgentCodex}, TerminalSize{})
		},
		func() (term.Tab, error) {
			return a.PanelBundleHandoff(engine.BundleHandoffRequest{BundleID: "b-1", Target: model.AgentOpenCode}, TerminalSize{})
		},
	} {
		tab, err := start()
		if err != nil {
			t.Fatal(err)
		}
		if tab.Host != "wsl:Ubuntu" || tab.Dir != "" {
			t.Errorf("tab = %+v", tab)
		}
	}
	want := append([]string{`C:\wsl.exe`}, launch.WSLExecArgs("Ubuntu", "panel script resume s1")...)
	if !slices.Equal(f.started[0].Argv, want) {
		t.Errorf("argv = %q, want %q", f.started[0].Argv, want)
	}
	if !slices.Equal(stub.calls, []string{"panel resume:s1", "panel handoff:s1", "panel bundle:b-1"}) {
		t.Errorf("server calls = %q", stub.calls)
	}
	if len(*opened) != 0 || *local != 0 {
		t.Errorf("the panel opened external terminals: %q, %d", *opened, *local)
	}

	tab, err := a.PanelShell(PanelShellRequest{Cwd: "/home/me/p"}, TerminalSize{})
	if err != nil || tab.Kind != term.KindShell || tab.Host != "wsl:Ubuntu" || tab.Dir != "/home/me/p" {
		t.Fatalf("shell = %+v, %v", tab, err)
	}
	if got := f.started[3].Argv; !slices.Equal(got, append([]string{`C:\wsl.exe`}, launch.WSLExecArgs("Ubuntu", launch.PanelShellScript("/home/me/p"))...)) {
		t.Errorf("shell argv = %q", got)
	}

	// Without WSL nothing is built on the distro.
	a.panelOverride.wslBinary = func() (string, error) { return "", launch.ErrWSLNotInstalled }
	if _, err := a.PanelResume(ref, TerminalSize{}); !errors.Is(err, launch.ErrWSLNotInstalled) {
		t.Errorf("without WSL: %v", err)
	}
	if len(stub.calls) != 3 || len(f.started) != 4 {
		t.Errorf("without WSL: calls %q, started %d", stub.calls, len(f.started))
	}
}

func sshPanelApp(t *testing.T, host string) (*App, *wslStub, *fakeTerminalManager) {
	stub := &wslStub{stubRemoteBackend: &stubRemoteBackend{}}
	a := NewApp()
	a.conn = newConnection(nil)
	a.conn.wantHost = host
	a.conn.localActive = false
	a.conn.client = stub
	a.conn.state = ConnectionState{Phase: ConnConnected, Host: host, Generation: 1}
	captureTerminal(a)
	return a, stub, withPanel(t, a)
}

func TestPanelSSH(t *testing.T) {
	a, stub, f := sshPanelApp(t, "remote-server")
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	tab, err := a.PanelResume(ref, TerminalSize{Cols: 90, Rows: 20})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Host != "remote-server" || tab.Kind != term.KindAgent {
		t.Errorf("tab = %+v", tab)
	}
	argv := f.started[0].Argv
	line := strings.Join(argv, " ")
	for _, want := range []string{"-o BatchMode=no", " -t ", "-- remote-server"} {
		if !strings.Contains(line, want) {
			t.Errorf("argv %q lacks %q", line, want)
		}
	}
	if got, want := argv[len(argv)-1], remote.QuoteArgs(launch.PosixBootstrapArgv("panel script resume s1")); got != want {
		t.Errorf("remote command = %q, want %q", got, want)
	}
	if env := strings.Join(f.started[0].Env, "\n"); strings.Contains(env, "AGENT_SESSIONS_ASKPASS") || !strings.Contains(env, "SSH_ASKPASS_REQUIRE=never") {
		t.Errorf("env uses askpass: %q", f.started[0].Env)
	}
	if !slices.Equal(stub.calls, []string{"panel resume:s1"}) {
		t.Errorf("server calls = %q", stub.calls)
	}
	// The external terminal still refuses SSH sessions.
	if err := a.OpenResumeInTerminal(ref); !errors.Is(err, ErrTerminalRemote) {
		t.Errorf("external: %v", err)
	}

	tab, err = a.PanelShell(PanelShellRequest{Cwd: "/srv/app"}, TerminalSize{})
	if err != nil || tab.Kind != term.KindShell {
		t.Fatalf("shell = %+v, %v", tab, err)
	}
	argv = f.started[1].Argv
	if got, want := argv[len(argv)-1], remote.QuoteArgs(launch.PosixBootstrapArgv(launch.PanelShellScript("/srv/app"))); got != want {
		t.Errorf("shell command = %q, want %q", got, want)
	}
}

func TestPanelSSHChecksBeforeBuilding(t *testing.T) {
	a, stub, f := sshPanelApp(t, "remote-server")
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	a.conn.state.Host, a.conn.wantHost = "-oProxyCommand=evil", "-oProxyCommand=evil"
	if _, err := a.PanelHandoff(HandoffRequest{Ref: ref, Target: model.AgentCodex}, TerminalSize{}); err == nil {
		t.Error("an unsafe host alias was accepted")
	}
	if len(stub.calls) != 0 || len(f.started) != 0 {
		t.Errorf("built %q and started %d for a host ssh cannot reach", stub.calls, len(f.started))
	}
}

func TestPanelSSHOffline(t *testing.T) {
	a := offlineRemoteApp()
	f := withPanel(t, a)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	if _, err := a.PanelResume(ref, TerminalSize{}); !errors.Is(err, rpc.ErrDisconnected) {
		t.Errorf("offline resume: %v", err)
	}
	// A shell needs no server.
	tab, err := a.PanelShell(PanelShellRequest{}, TerminalSize{})
	if err != nil || tab.Host != "remote-server" {
		t.Errorf("offline shell = %+v, %v", tab, err)
	}
	if len(f.started) != 1 {
		t.Errorf("started %d", len(f.started))
	}
}

func TestPanelShellLocal(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	f := withPanel(t, a)
	dir := t.TempDir()
	if _, err := a.PanelShell(PanelShellRequest{Cwd: dir}, TerminalSize{}); err != nil {
		t.Fatal(err)
	}
	if spec := f.started[0]; !slices.Equal(spec.Argv, []string{"/bin/fake-shell", "-l"}) || spec.Dir != dir || !slices.Equal(spec.Env, []string{"LOGIN=1"}) {
		t.Errorf("spec = %+v", spec)
	}
	home, _ := a.panelOverride.home()
	for _, cwd := range []string{"", filepath.Join(dir, "missing")} {
		if _, err := a.PanelShell(PanelShellRequest{Cwd: cwd}, TerminalSize{}); err != nil {
			t.Fatal(err)
		}
		if got := f.started[len(f.started)-1].Dir; got != home {
			t.Errorf("cwd %q: dir = %q, want home %q", cwd, got, home)
		}
		if got := f.tabs[len(f.tabs)-1].Dir; got != home {
			t.Errorf("cwd %q: tab dir = %q, want home %q", cwd, got, home)
		}
	}
	if f.tabs[0].Kind != term.KindShell || f.tabs[0].Host != "" {
		t.Errorf("tab = %+v", f.tabs[0])
	}
}

func TestTerminalBindings(t *testing.T) {
	a := NewApp()
	f := withPanel(t, a)
	if err := a.TerminalWrite("t1", "ls\r"); err != nil {
		t.Fatal(err)
	}
	if err := a.TerminalResize("t1", 100, 30); err != nil {
		t.Fatal(err)
	}
	a.TerminalAck("t1", 42)
	if err := a.TerminalClose("t1"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"t1:ls\r"}) || f.resizes[0] != [2]int{100, 30} || f.acks[0] != 42 || f.closed[0] != "t1" {
		t.Errorf("fake = %+v", f)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if f.closeAll != 1 {
		t.Error("Close did not end the terminals")
	}
}

func TestTerminalDropFiles(t *testing.T) {
	a := NewApp()
	f := withPanel(t, a)
	f.tabs = []term.Tab{{ID: "t1"}, {ID: "t2", Host: "box"}, {ID: "t3", Host: remote.WSLTarget("Ubuntu")}}
	if runtime.GOOS != "windows" {
		got, err := a.TerminalDropFiles("t1", []string{"/home/me/shot.png", "/home/me/my shot.png"})
		if err != nil || !slices.Equal(got, []string{"/home/me/shot.png", "'/home/me/my shot.png'"}) {
			t.Errorf("local = %q, %v", got, err)
		}
	}
	got, err := a.TerminalDropFiles("t2", []string{"/home/me/shot.png", "/home/me/it's.png"})
	if err != nil || !slices.Equal(got, []string{"/drop/box/shot.png", `'/drop/box/it'\''s.png'`}) {
		t.Errorf("ssh = %q, %v", got, err)
	}
	got, err = a.TerminalDropFiles("t3", []string{"/x/a.png"})
	if err != nil || !slices.Equal(got, []string{"/drop/wsl:Ubuntu/a.png"}) {
		t.Errorf("wsl = %q, %v", got, err)
	}
	if _, err := a.TerminalDropFiles("t9", []string{"/x/a.png"}); !errors.Is(err, term.ErrNotFound) {
		t.Errorf("unknown tab: %v", err)
	}
	failed := errors.New("upload failed")
	a.panelOverride.dropFiles = func(context.Context, string, remote.SSHOptions, []string) ([]string, error) { return nil, failed }
	if _, err := a.TerminalDropFiles("t2", []string{"/x/a.png"}); !errors.Is(err, failed) {
		t.Errorf("failed upload: %v", err)
	}
	a.terminals = nil
	if _, err := a.TerminalDropFiles("t1", []string{"/x/a.png"}); !errors.Is(err, ErrPanelUnavailable) {
		t.Errorf("no panel: %v", err)
	}
}

func TestTypedPath(t *testing.T) {
	for _, tc := range []struct {
		path    string
		windows bool
		want    string
	}{
		{"/home/me/shot-1_a.png", false, "/home/me/shot-1_a.png"},
		{"/home/me/my shot.png", false, "'/home/me/my shot.png'"},
		{"/tmp/$HOME(1).png", false, "'/tmp/$HOME(1).png'"},
		{"/tmp/it's.png", false, `'/tmp/it'\''s.png'`},
		{`C:\Users\me\shot.png`, true, `C:\Users\me\shot.png`},
		{`C:\Users\me\my shot.png`, true, `"C:\Users\me\my shot.png"`},
		{`C:\Users\me\a&b.png`, true, `"C:\Users\me\a&b.png"`},
		{`C:\Users\me\ảnh.png`, true, `"C:\Users\me\ảnh.png"`},
	} {
		if got := typedPath(tc.path, tc.windows); got != tc.want {
			t.Errorf("typedPath(%q, %v) = %q, want %q", tc.path, tc.windows, got, tc.want)
		}
	}
}

func TestSetTerminalOpen(t *testing.T) {
	a := NewApp()
	withPanel(t, a)
	path := filepath.Join(a.roots.Config, "config.toml")
	if err := os.WriteFile(path, []byte("[terminal]\napp = \"kitty\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := a.SetTerminalOpen(OpenInExternal)
	if err != nil || info.Open != OpenInExternal || a.LaunchInfo().Open != OpenInExternal {
		t.Fatalf("SetTerminalOpen = %+v, %v", info, err)
	}
	data, _ := os.ReadFile(path)
	if app, _ := configfile.Value(data, "terminal", "app"); app != "kitty" {
		t.Errorf("the terminal app was lost: %s", data)
	}
	if info, err := a.SetTerminalOpen(OpenInPanel); err != nil || info.Open != OpenInPanel {
		t.Errorf("back to the panel = %+v, %v", info, err)
	}
	if _, err := a.SetTerminalOpen("tmux"); err == nil {
		t.Error("an unknown mode was saved")
	}
}

func TestReleaseHostSkipsHostWithTerminals(t *testing.T) {
	c := newConnection(nil)
	var stopped []string
	c.stopMaster = func(_ context.Context, alias string, _ remote.SSHOptions) error {
		stopped = append(stopped, alias)
		return nil
	}
	c.setBusy(func(host string) bool { return host == "busy" })
	opts := remote.SSHOptions{ControlMaster: "auto"}
	c.releaseHost("busy", opts)
	c.releaseHost("idle", opts)
	if !slices.Equal(stopped, []string{"idle"}) {
		t.Errorf("stopped %q, want only the idle host", stopped)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
