package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func linuxEnv(home string, onPath map[string]string, vars map[string]string) TerminalEnv {
	return TerminalEnv{
		GOOS:   "linux",
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(name string) (string, error) {
			if p, ok := onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Home: home,
	}
}

func ids(apps []TerminalApp) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.ID)
	}
	return out
}

func TestDetectLinux(t *testing.T) {
	platform.SkipWithoutModeBits(t)
	home := t.TempDir()
	kitty := filepath.Join(home, ".local", "bin", "kitty")
	if err := os.MkdirAll(filepath.Dir(kitty), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kitty, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	// Not executable: not a terminal.
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "foot"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	onPath := map[string]string{"konsole": "/usr/bin/konsole", "kgx": "/usr/bin/kgx", "gnome-terminal": "/usr/bin/gnome-terminal", "ptyxis": "/usr/bin/ptyxis"}
	apps := linuxEnv(home, onPath, nil).Detect()
	if got, want := ids(apps), []string{"gnome-terminal", "ptyxis", "kgx", "konsole", "kitty"}; !slices.Equal(got, want) {
		t.Fatalf("Detect = %q, want %q", got, want)
	}
	if apps[0].Path != "/usr/bin/gnome-terminal" || apps[0].Name != "GNOME Terminal" || apps[4].Path != kitty {
		t.Errorf("apps = %+v", apps)
	}

	for _, c := range []struct {
		vars map[string]string
		want string
	}{
		{nil, "gnome-terminal"},
		{map[string]string{"XDG_CURRENT_DESKTOP": "ubuntu:GNOME"}, "ptyxis"},
		{map[string]string{"XDG_CURRENT_DESKTOP": "KDE"}, "konsole"},
		{map[string]string{"XDG_CURRENT_DESKTOP": "XFCE"}, "gnome-terminal"}, // xfce4-terminal not installed
		{map[string]string{"XDG_CURRENT_DESKTOP": "KDE", "TERMINAL": "/usr/local/bin/kitty"}, "kitty"},
		{map[string]string{"TERMINAL": "st"}, "gnome-terminal"},
	} {
		got, ok := linuxEnv(home, onPath, c.vars).Auto(apps)
		if !ok || got.ID != c.want {
			t.Errorf("Auto(%v) = %q, want %q", c.vars, got.ID, c.want)
		}
	}
	if _, ok := linuxEnv(home, nil, nil).Auto(nil); ok {
		t.Error("Auto chose from nothing")
	}
}

func TestDetectMac(t *testing.T) {
	sys, user := t.TempDir(), t.TempDir()
	bundle := func(dir, app, sdef string) {
		t.Helper()
		res := filepath.Join(dir, app, "Contents", "Resources")
		if err := os.MkdirAll(res, 0o755); err != nil {
			t.Fatal(err)
		}
		if sdef != "" {
			if err := os.WriteFile(filepath.Join(res, "App.sdef"), []byte(sdef), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	env := TerminalEnv{GOOS: "darwin", Getenv: func(string) string { return "" }, AppDirs: []string{sys, user}}
	bundle(sys, "Terminal.app", "")
	// Ghostty before 1.3 has no scripting dictionary.
	bundle(user, "Ghostty.app", "")
	// An empty folder named like an app is not one.
	if err := os.MkdirAll(filepath.Join(sys, "iTerm.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ids(env.Detect()); !slices.Equal(got, []string{"terminal"}) {
		t.Fatalf("Detect = %q", got)
	}

	bundle(user, "iTerm.app", `<command name="create window with default profile" code="Itrmnwwn">`)
	bundle(user, "Ghostty.app", `<record-type name="surface configuration"><property name="initial working directory"/><property name="initial input"/>`)
	apps := env.Detect()
	if got := ids(apps); !slices.Equal(got, []string{"terminal", "iterm2", "ghostty"}) {
		t.Fatalf("Detect = %q", got)
	}
	if apps[1].Path != filepath.Join(user, "iTerm.app") || apps[2].Name != "Ghostty" {
		t.Errorf("apps = %+v", apps)
	}
	if got, _ := env.Auto(apps[1:]); got.ID != "iterm2" {
		t.Errorf("Auto without Terminal = %q", got.ID)
	}
	if got, _ := env.Auto(apps); got.ID != "terminal" {
		t.Errorf("Auto = %q", got.ID)
	}
}

func TestResolveTerminal(t *testing.T) {
	env := linuxEnv("", nil, nil)
	apps := []TerminalApp{{ID: "konsole", Name: "Konsole"}, {ID: "kitty", Name: "kitty"}}
	if got, err := env.Resolve(apps, ""); err != nil || got.ID != "konsole" {
		t.Errorf("auto: %+v, %v", got, err)
	}
	if got, err := env.Resolve(apps, "kitty"); err != nil || got.ID != "kitty" {
		t.Errorf("chosen: %+v, %v", got, err)
	}
	// A chosen terminal that is gone is an error, never a substitute.
	var missing *MissingTerminalError
	if _, err := env.Resolve(apps, "ghostty"); !errors.As(err, &missing) || missing.Name != "Ghostty" || !strings.Contains(err.Error(), "Settings") {
		t.Errorf("missing: %v", err)
	}
	if _, err := env.Resolve(apps, "other"); !errors.As(err, &missing) || missing.Name != "other" {
		t.Errorf("unknown: %v", err)
	}
	if _, err := env.Resolve(nil, ""); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("none: %v", err)
	}
}

// fakeLauncher records what it would start instead of starting it.
type fakeLauncher struct {
	started []*exec.Cmd
	scripts [][]string
	apps    []string
	args    [][]string
}

func (f *fakeLauncher) launcher(goos string, exe string) terminalLauncher {
	return terminalLauncher{
		goos:  goos,
		find:  func(string) (Executable, error) { return Executable{Path: exe}, nil },
		shell: "/bin/bash",
		start: func(c *exec.Cmd) error {
			f.started = append(f.started, c)
			return nil
		},
		osascript: func(app string, script, args []string) error {
			f.apps = append(f.apps, app)
			f.scripts = append(f.scripts, script)
			f.args = append(f.args, args)
			return nil
		},
	}
}

func TestLinuxTerminalArgs(t *testing.T) {
	dir := platform.TempDir(t)
	cmd := provider.Command{Argv: []string{"claude", "--resume", "abc"}, Dir: dir}
	script := posixScript([]string{"/opt/bin/claude", "--resume", "abc"}, dir, "/bin/bash")
	sh := []string{"/bin/sh", "-c", script}
	want := map[string][]string{
		"gnome-terminal": {"--window", "--working-directory=" + dir, "--"},
		"kgx":            {"--working-directory=" + dir, "--"},
		"konsole":        {"--separate", "--workdir", dir, "-e"},
		"xfce4-terminal": {"--working-directory=" + dir, "-x"},
		"ghostty":        {"--working-directory=" + dir, "-e"},
		"kitty":          {"--directory", dir},
		"wezterm":        {"start", "--always-new-process", "--cwd", dir, "--"},
		"alacritty":      {"--working-directory", dir, "-e"},
		"foot":           {"--working-directory=" + dir},
		"xterm":          {"-e"},
	}
	for _, k := range terminalKinds {
		if k.goos != "linux" {
			continue
		}
		f := &fakeLauncher{}
		path := "/usr/bin/" + k.exe
		if err := f.launcher("linux", "/opt/bin/claude").open(TerminalApp{ID: k.id, Name: k.name, Path: path}, cmd); err != nil {
			t.Fatalf("%s: %v", k.id, err)
		}
		c := f.started[0]
		if c.Path != path || c.Args[0] != path || c.Dir != dir {
			t.Errorf("%s: path %q args[0] %q dir %q", k.id, c.Path, c.Args[0], c.Dir)
		}
		if k.id == "ptyxis" {
			wantArgs := []string{path, "--new-window", "--working-directory=" + dir, "-x", JoinCommand(sh)}
			if !slices.Equal(c.Args, wantArgs) {
				t.Errorf("ptyxis args = %q", c.Args)
			}
			continue
		}
		w, ok := want[k.id]
		if !ok {
			t.Errorf("%s: no expected arguments", k.id)
			continue
		}
		if got := c.Args; !slices.Equal(got, append(append([]string{path}, w...), sh...)) {
			t.Errorf("%s args = %q", k.id, got)
		}
	}
}

func TestMacTerminalScripts(t *testing.T) {
	dir := platform.TempDir(t)
	cmd := provider.Command{Argv: []string{"codex", "resume", "$(id)"}, Dir: dir}
	line := bootstrapLine(posixScript([]string{"/opt/bin/codex", "resume", "$(id)"}, dir, "/bin/bash"))
	for _, c := range []struct {
		id   string
		args []string
	}{
		{"terminal", []string{line}},
		{"iterm2", []string{line}},
		{"ghostty", []string{dir, line + "\n"}},
	} {
		f := &fakeLauncher{}
		k, _ := terminalKindFor("darwin", c.id)
		if err := f.launcher("darwin", "/opt/bin/codex").open(TerminalApp{ID: c.id, Name: k.name, Path: "/Applications/X.app"}, cmd); err != nil {
			t.Fatal(err)
		}
		if len(f.started) != 0 || f.apps[0] != k.name || !slices.Equal(f.args[0], c.args) {
			t.Errorf("%s: app %q args %q", c.id, f.apps, f.args)
		}
		// The script is fixed: user values are only ever its arguments.
		if !slices.Equal(f.scripts[0], k.script) || strings.Contains(strings.Join(k.script, "\n"), dir) {
			t.Errorf("%s: script %q", c.id, f.scripts[0])
		}
	}
}

func TestOpenInChecksCommand(t *testing.T) {
	dir := platform.TempDir(t)
	f := &fakeLauncher{}
	l := f.launcher("linux", "/opt/bin/claude")
	app := TerminalApp{ID: "kitty", Name: "kitty", Path: "/usr/bin/kitty"}
	for name, cmd := range map[string]provider.Command{
		"no argv":     {Dir: dir},
		"no dir":      {Argv: []string{"claude"}},
		"missing dir": {Argv: []string{"claude"}, Dir: filepath.Join(dir, "gone")},
		"NUL":         {Argv: []string{"claude", "a\x00b"}, Dir: dir},
	} {
		if err := l.open(app, cmd); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	l.find = func(string) (Executable, error) { return Executable{}, ErrAgentNotFound }
	if err := l.open(app, provider.Command{Argv: []string{"claude"}, Dir: dir}); !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("not found: %v", err)
	}
	if err := l.open(TerminalApp{ID: "iterm2", Name: "iTerm2"}, provider.Command{Argv: []string{"claude"}, Dir: dir}); err == nil {
		t.Error("a macOS terminal opened on Linux")
	}
	if len(f.started) != 0 {
		t.Errorf("started %d terminals", len(f.started))
	}
}

// posixTrickyArgs survive a shell only if every argument is quoted correctly.
var posixTrickyArgs = []string{"resume", "it's", `"q"`, "$HOME", "`id`", `back\slash`, "line1\nline2", "", "-dash", "ünï", "100%", "!bang", "a b\tc"}

// recordingAgent writes a script that records its directory, PATH and
// arguments into out.
func recordingAgent(t *testing.T, out string) string {
	t.Helper()
	bin := filepath.Join(platform.TempDir(t), "node bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(bin, "codex")
	body := "#!/bin/sh\npwd > '" + out + ".pwd'\nprintf '%s' \"$PATH\" > '" + out + ".path'\nprintf '%s\\0' \"$@\" > '" + out + ".args'\nexit \"${AGENT_STATUS:-0}\"\n"
	if err := os.WriteFile(agent, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return agent
}

func checkRecorded(t *testing.T, out, dir, bin string, args []string) {
	t.Helper()
	pwd, err := os.ReadFile(out + ".pwd")
	if err != nil {
		t.Fatalf("agent did not run: %v", err)
	}
	if got := strings.TrimSuffix(string(pwd), "\n"); got != dir {
		t.Errorf("pwd = %q, want %q", got, dir)
	}
	path, _ := os.ReadFile(out + ".path")
	if !strings.HasPrefix(string(path), bin+":") {
		t.Errorf("PATH = %q, want %q first", path, bin)
	}
	raw, _ := os.ReadFile(out + ".args")
	got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if !slices.Equal(got, args) {
		t.Errorf("args = %q, want %q", got, args)
	}
}

func posixTrickyDir(t *testing.T) string {
	dir := filepath.Join(platform.TempDir(t), `it's "a" $HOME `+"`x`"+` \ ünï !dir`)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPosixScriptRunsAgentExactly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "out")
	agent := recordingAgent(t, out)
	argv := append([]string{agent}, posixTrickyArgs...)
	script := posixScript(argv, dir, "/bin/true")

	// As Linux terminals run it: an argv vector.
	c := exec.Command("/bin/sh", "-c", script)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	checkRecorded(t, out, dir, filepath.Dir(agent), posixTrickyArgs)

	// As Ptyxis gets it: one string split with POSIX quoting rules.
	_ = os.Remove(out + ".args")
	c = exec.Command("/bin/sh", "-c", "exec "+JoinCommand([]string{"/bin/sh", "-c", script}))
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	checkRecorded(t, out, dir, filepath.Dir(agent), posixTrickyArgs)

	// A failing agent is reported before the shell takes over.
	c = exec.Command("/bin/sh", "-c", script)
	c.Env = append(os.Environ(), "AGENT_STATUS=3")
	b, err := c.CombinedOutput()
	if err != nil || !strings.Contains(string(b), "codex exited with status 3.") {
		t.Errorf("failure report: %v, %q", err, b)
	}
}

func TestPosixScriptStopsOutsideDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "out")
	script := posixScript([]string{recordingAgent(t, out), "x"}, dir, "/bin/true")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("/bin/sh", "-c", script)
	if err := c.Run(); err == nil {
		t.Error("script succeeded without its directory")
	}
	if _, err := os.Stat(out + ".pwd"); err == nil {
		t.Error("agent ran outside its directory")
	}
}

func TestBootstrapLineInUserShells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "out")
	agent := recordingAgent(t, out)
	line := bootstrapLine(posixScript(append([]string{agent}, posixTrickyArgs...), dir, "/bin/true"))
	// Nothing in the line is special to any shell inside single quotes.
	if strings.ContainsAny(line, "\n%!") || strings.Count(line, "'") != 4 || strings.Contains(line, `\\`) {
		t.Fatalf("line has special characters: %s", line)
	}
	ran := 0
	for _, shell := range []string{"sh", "bash", "zsh", "fish", "tcsh"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		ran++
		for _, f := range []string{".pwd", ".path", ".args"} {
			_ = os.Remove(out + f)
		}
		c := exec.Command(path, "-c", line)
		if b, err := c.CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", shell, err, b)
			continue
		}
		checkRecorded(t, out, dir, filepath.Dir(agent), posixTrickyArgs)
	}
	if ran == 0 {
		t.Skip("no shells")
	}
}

func TestLoginShell(t *testing.T) {
	platform.RequireCommand(t, "sh")
	sh, _ := exec.LookPath("sh")
	sh, _ = filepath.Abs(sh)
	env := func(v string) func(string) string { return func(string) string { return v } }
	if got := loginShell("linux", env(sh)); got != sh {
		t.Errorf("SHELL ignored: %q", got)
	}
	for _, bad := range []string{"", "zsh", "/nonexistent/zsh"} {
		if got := loginShell("darwin", env(bad)); got != "/bin/zsh" {
			t.Errorf("darwin %q: %q", bad, got)
		}
		if got := loginShell("linux", env(bad)); got != "/bin/sh" {
			t.Errorf("linux %q: %q", bad, got)
		}
	}
}

// fakeOSAScript replaces osascript with a shell script.
func fakeOSAScript(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(platform.TempDir(t), "osascript")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := osascriptPath
	osascriptPath = path
	t.Cleanup(func() { osascriptPath = old })
}

func TestRunOSAScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	out := filepath.Join(platform.TempDir(t), "args")
	fakeOSAScript(t, `printf '%s\0' "$@" > '`+out+`'`)
	if err := runOSAScript("Terminal", []string{"on run argv", "end run"}, []string{"exec x", "$y"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"); !slices.Equal(got, []string{"-e", "on run argv", "-e", "end run", "exec x", "$y"}) {
		t.Errorf("osascript args = %q", got)
	}

	for stderr, want := range map[string]string{
		"execution error: Not authorized to send Apple events to Terminal. (-1743)":       "Privacy & Security > Automation",
		"execution error: Ghostty got an error: Can't get surface configuration. (-1728)": "choose another terminal",
		"line one\nexecution error: something else (-2700)":                               "Terminal could not open a window: execution error: something else (-2700)",
	} {
		msg := filepath.Join(platform.TempDir(t), "stderr")
		if err := os.WriteFile(msg, []byte(stderr+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		fakeOSAScript(t, "cat '"+msg+"' >&2; exit 1")
		if err := runOSAScript("Terminal", nil, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("stderr %q: %v", stderr, err)
		}
	}
}

func TestRunOSAScriptWaitsForPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	done := filepath.Join(platform.TempDir(t), "done")
	fakeOSAScript(t, "sleep 1; : > '"+done+"'")
	old := osascriptWait
	osascriptWait = 100 * time.Millisecond
	t.Cleanup(func() { osascriptWait = old })

	err := runOSAScript("Ghostty", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "allow it") {
		t.Fatalf("err = %v", err)
	}
	// osascript is left to finish once the user answers: never killed.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(done); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("osascript was stopped")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
