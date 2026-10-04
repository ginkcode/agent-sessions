package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

type openedIn struct {
	terminal launch.TerminalApp
	cmd      provider.Command
}

// fakeTerminals gives a a temp config dir and a machine with the named
// Linux terminals installed, and records what it opens in them.
func fakeTerminals(t *testing.T, a *App, installed ...string) *[]openedIn {
	t.Helper()
	if !launch.ChooseTerminal() {
		t.Skip("the terminal is fixed on this platform")
	}
	a.roots.Config = t.TempDir()
	onPath := map[string]bool{}
	for _, id := range installed {
		onPath[id] = true
	}
	a.terminalEnv = &launch.TerminalEnv{
		GOOS:   "linux",
		Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			if onPath[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
	}
	var opened []openedIn
	a.openInOverride = func(term launch.TerminalApp, cmd provider.Command) error {
		opened = append(opened, openedIn{term, cmd})
		return nil
	}
	return &opened
}

func TestTerminalSettings(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	fakeTerminals(t, a, "konsole", "kitty")

	s, err := a.TerminalSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Choose || s.Selected != "" || s.Missing || s.Auto == nil || s.Auto.ID != "konsole" || len(s.Options) != 2 || s.Options[1] != (TerminalOption{"kitty", "kitty"}) || s.Hint != "" {
		t.Errorf("settings = %+v", s)
	}
	mac := launch.TerminalEnv{GOOS: "darwin"}
	if s := terminalSettings(mac, []launch.TerminalApp{{ID: "terminal", Name: "Terminal"}}, ""); s.Hint == "" || s.Auto.ID != "terminal" {
		t.Errorf("mac settings = %+v", s)
	}

	s, err = a.SetTerminal("kitty")
	if err != nil || s.Selected != "kitty" || s.SelectedName != "kitty" || s.Missing {
		t.Fatalf("SetTerminal = %+v, %v", s, err)
	}
	if s, _ := a.TerminalSettings(); s.Selected != "kitty" {
		t.Errorf("not saved: %+v", s)
	}
	data, _ := os.ReadFile(filepath.Join(a.roots.Config, "config.toml"))
	if string(data) != "[terminal]\napp = \"kitty\"\n" {
		t.Errorf("config = %q", data)
	}

	// Only an installed terminal can be chosen.
	for _, id := range []string{"ghostty", "/bin/sh", "kitty -e x"} {
		if _, err := a.SetTerminal(id); err == nil {
			t.Errorf("SetTerminal(%q) accepted", id)
		}
	}
	if s, err := a.SetTerminal(""); err != nil || s.Selected != "" {
		t.Errorf("Automatic: %+v, %v", s, err)
	}
	if s, _ := a.TerminalSettings(); s.Selected != "" {
		t.Errorf("Automatic not saved: %+v", s)
	}
}

func TestOpenInChosenTerminal(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	opened := fakeTerminals(t, a, "konsole", "kitty")
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}

	if !a.LaunchInfo().Terminal || !a.LaunchInfo().ChooseTerminal {
		t.Errorf("LaunchInfo = %+v", a.LaunchInfo())
	}
	if err := a.OpenResumeInTerminal(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetTerminal("kitty"); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenResumeInTerminal(ref); err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 2 || (*opened)[0].terminal.ID != "konsole" || (*opened)[1].terminal.ID != "kitty" ||
		(*opened)[1].terminal.Path != "/usr/bin/kitty" || (*opened)[1].cmd.Argv[0] != "fake" || (*opened)[1].cmd.Dir != "/tmp/work" {
		t.Errorf("opened %+v", *opened)
	}
}

func TestOpenInMissingTerminalWritesNothing(t *testing.T) {
	a, svc, _ := setupHandoffTest(t)
	opened := fakeTerminals(t, a, "konsole", "kitty")
	if _, err := a.SetTerminal("kitty"); err != nil {
		t.Fatal(err)
	}
	// kitty is uninstalled; konsole must not be used in its place.
	env := *a.terminalEnv
	env.LookPath = func(name string) (string, error) {
		if name == "konsole" {
			return "/usr/bin/konsole", nil
		}
		return "", errors.New("not found")
	}
	a.terminalEnv = &env

	if !a.LaunchInfo().Terminal {
		t.Error("a missing choice hides Open in terminal instead of explaining")
	}
	if s, _ := a.TerminalSettings(); !s.Missing || s.SelectedName != "kitty" || s.Auto.ID != "konsole" {
		t.Errorf("settings = %+v", s)
	}
	err := a.OpenHandoffInTerminal(HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: model.AgentOpenCode,
		CWD:    "/custom/dir",
	})
	var missing *launch.MissingTerminalError
	if !errors.As(err, &missing) || missing.ID != "kitty" {
		t.Fatalf("err = %v, want MissingTerminalError", err)
	}
	if len(*opened) != 0 {
		t.Errorf("opened %+v", *opened)
	}
	for _, f := range []func(string, string) (string, error){handoff.PromptFilePath, handoff.ContextFilePath} {
		p, _ := f(svc.DataDir(), "sess-root")
		if _, err := os.Stat(p); err == nil {
			t.Errorf("handoff file written for a terminal that cannot open: %s", p)
		}
	}
}

func TestLaunchInfoWithoutTerminals(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	fakeTerminals(t, a)
	info := a.LaunchInfo()
	if info.Terminal || !info.ChooseTerminal {
		t.Errorf("LaunchInfo = %+v", info)
	}
	if err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}); !errors.Is(err, launch.ErrNoTerminal) {
		t.Errorf("err = %v", err)
	}
	// An unreadable config is reported when clicked, not hidden.
	if err := os.Mkdir(filepath.Join(a.roots.Config, "config.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !a.LaunchInfo().Terminal {
		t.Error("Terminal = false with an unreadable config")
	}
	if err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"}); err == nil || !strings.Contains(err.Error(), "read settings") {
		t.Errorf("err = %v", err)
	}
}

func TestTerminalSettingsWhileRemote(t *testing.T) {
	stub := &stubRemoteBackend{resumeCmd: "claude --resume s1"}
	for name, a := range map[string]*App{"connected": setupRemoteApp(stub), "offline": offlineRemoteApp()} {
		opened := fakeTerminals(t, a, "kitty")
		if s, err := a.SetTerminal("kitty"); err != nil || s.Selected != "kitty" {
			t.Errorf("%s: SetTerminal = %+v, %v", name, s, err)
		}
		if s, err := a.TerminalSettings(); err != nil || s.Selected != "kitty" {
			t.Errorf("%s: TerminalSettings = %+v, %v", name, s, err)
		}
		if err := a.OpenResumeInTerminal(model.SessionRef{Agent: model.AgentClaude, ID: "s1"}); !errors.Is(err, ErrTerminalRemote) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(*opened) != 0 {
			t.Errorf("%s: opened %+v", name, *opened)
		}
	}
}

// The App and the engine's manage settings write one file; neither may lose
// the other's change.
func TestSetTerminalKeepsManageSettings(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	fakeTerminals(t, a, "konsole", "kitty")
	path := filepath.Join(a.roots.Config, "config.toml")
	store := manage.NewConfigStore(path)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := store.Update(func(c *manage.Config) { c.Enabled = true; c.AllowRestore = i%2 == 0 })
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := a.SetTerminal([]string{"konsole", "kitty"}[i%2])
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := store.Load()
	if err != nil || !cfg.Enabled {
		t.Errorf("manage settings lost: %+v, %v", cfg, err)
	}
	data, _ := os.ReadFile(path)
	if id, ok := configfile.Value(data, "terminal", "app"); !ok || (id != "konsole" && id != "kitty") {
		t.Errorf("terminal lost:\n%s", data)
	}
	if strings.Count(string(data), "[terminal]") != 1 || strings.Count(string(data), "[manage]") != 1 {
		t.Errorf("config:\n%s", data)
	}
}
