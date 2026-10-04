package launch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

// TerminalApp is a terminal emulator installed on this machine.
type TerminalApp struct {
	ID   string
	Name string
	// Path is the executable on Linux and the .app bundle on macOS.
	Path string
}

// ErrNoTerminal means no terminal app the launcher supports is installed.
var ErrNoTerminal = errors.New("no supported terminal app was found; install one, or use Copy command")

// MissingTerminalError means the terminal chosen in Settings is no longer
// installed. Another terminal is never used in its place.
type MissingTerminalError struct{ ID, Name string }

func (e *MissingTerminalError) Error() string {
	return e.Name + " was not found. Choose another terminal in Settings, or use Copy command"
}

// terminalKind is a terminal the launcher knows how to start a command in.
type terminalKind struct {
	id, name, goos string
	// Linux: the executable name, and the arguments that open a new window
	// in dir running sh, an argv starting with /bin/sh.
	exe  string
	args func(dir string, sh []string) []string
	// macOS: the bundle directory, the terms its scripting dictionary must
	// define, an AppleScript run with the arguments scriptArgs returns.
	app        string
	sdef       []string
	script     []string
	scriptArgs func(dir, line string) []string
}

func linuxTerminal(id, name string, args func(dir string, sh []string) []string) terminalKind {
	return terminalKind{id: id, name: name, goos: "linux", exe: id, args: args}
}

// prepend returns a new slice of head followed by sh.
func prepend(sh []string, head ...string) []string {
	return append(head, sh...)
}

// terminalKinds is the catalog, in the order Settings lists what it finds.
// Each Linux entry passes the command as an argv vector; flags are from each
// terminal's documentation. Each macOS entry only types one line into a new
// window through the app's scripting dictionary; the values arrive as
// arguments of the fixed script, never inside its source.
var terminalKinds = []terminalKind{
	linuxTerminal("gnome-terminal", "GNOME Terminal", func(dir string, sh []string) []string {
		return prepend(sh, "--window", "--working-directory="+dir, "--")
	}),
	// Ptyxis splits -x with g_shell_parse_argv, which follows POSIX quoting.
	linuxTerminal("ptyxis", "Ptyxis", func(dir string, sh []string) []string {
		return []string{"--new-window", "--working-directory=" + dir, "-x", JoinCommand(sh)}
	}),
	linuxTerminal("kgx", "GNOME Console", func(dir string, sh []string) []string {
		return prepend(sh, "--working-directory="+dir, "--")
	}),
	linuxTerminal("konsole", "Konsole", func(dir string, sh []string) []string {
		return prepend(sh, "--separate", "--workdir", dir, "-e")
	}),
	linuxTerminal("xfce4-terminal", "Xfce Terminal", func(dir string, sh []string) []string {
		return prepend(sh, "--working-directory="+dir, "-x")
	}),
	linuxTerminal("ghostty", "Ghostty", func(dir string, sh []string) []string {
		return prepend(sh, "--working-directory="+dir, "-e")
	}),
	linuxTerminal("kitty", "kitty", func(dir string, sh []string) []string {
		return prepend(sh, "--directory", dir)
	}),
	linuxTerminal("wezterm", "WezTerm", func(dir string, sh []string) []string {
		return prepend(sh, "start", "--always-new-process", "--cwd", dir, "--")
	}),
	linuxTerminal("alacritty", "Alacritty", func(dir string, sh []string) []string {
		return prepend(sh, "--working-directory", dir, "-e")
	}),
	linuxTerminal("foot", "foot", func(dir string, sh []string) []string {
		return prepend(sh, "--working-directory="+dir)
	}),
	// xterm has no directory flag; it starts in the process's directory.
	linuxTerminal("xterm", "XTerm", func(_ string, sh []string) []string {
		return prepend(sh, "-e")
	}),
	{
		id: "terminal", name: "Terminal", goos: "darwin", app: "Terminal.app",
		script: []string{
			"on run argv",
			`tell application id "com.apple.Terminal"`,
			"do script (item 1 of argv)",
			"activate",
			"end tell",
			"end run",
		},
		scriptArgs: func(_, line string) []string { return []string{line} },
	},
	{
		id: "iterm2", name: "iTerm2", goos: "darwin", app: "iTerm.app",
		sdef: []string{"create window with default profile"},
		script: []string{
			"on run argv",
			`tell application id "com.googlecode.iterm2"`,
			"set w to (create window with default profile)",
			"tell current session of w to write text (item 1 of argv)",
			"activate",
			"end tell",
			"end run",
		},
		scriptArgs: func(_, line string) []string { return []string{line} },
	},
	// Ghostty 1.3 added AppleScript. initial input is typed like the other
	// apps' line; command would keep the window open with Ghostty's own
	// wait-after-command prompt.
	{
		id: "ghostty", name: "Ghostty", goos: "darwin", app: "Ghostty.app",
		sdef: []string{"surface configuration", "initial working directory", "initial input"},
		script: []string{
			"on run argv",
			`tell application id "com.mitchellh.ghostty"`,
			"set cfg to new surface configuration",
			"set initial working directory of cfg to (item 1 of argv)",
			"set initial input of cfg to (item 2 of argv)",
			"new window with configuration cfg",
			"activate",
			"end tell",
			"end run",
		},
		scriptArgs: func(dir, line string) []string { return []string{dir, line + "\n"} },
	},
}

func terminalKindFor(goos, id string) (terminalKind, bool) {
	for _, k := range terminalKinds {
		if k.goos == goos && k.id == id {
			return k, true
		}
	}
	return terminalKind{}, false
}

// ChooseTerminal reports whether the terminal can be chosen in Settings. On
// Windows the console always uses Windows PowerShell.
func ChooseTerminal() bool { return terminalSupported && runtime.GOOS != "windows" }

// TerminalEnv is where terminal apps are looked for.
type TerminalEnv struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Home     string
	// AppDirs are the directories searched for macOS app bundles.
	AppDirs []string
}

// SystemTerminalEnv is this machine's environment.
func SystemTerminalEnv() TerminalEnv {
	home, _ := os.UserHomeDir()
	env := TerminalEnv{
		GOOS:     runtime.GOOS,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Home:     home,
		AppDirs:  []string{"/Applications", "/Applications/Utilities", "/System/Applications/Utilities"},
	}
	if home != "" {
		env.AppDirs = append(env.AppDirs, filepath.Join(home, "Applications"))
	}
	return env
}

// Detect lists the supported terminal apps installed, in catalog order. It
// only looks at files: nothing is run.
func (e TerminalEnv) Detect() []TerminalApp {
	var apps []TerminalApp
	for _, k := range terminalKinds {
		if k.goos != e.GOOS {
			continue
		}
		if path := e.locate(k); path != "" {
			apps = append(apps, TerminalApp{ID: k.id, Name: k.name, Path: path})
		}
	}
	return apps
}

func (e TerminalEnv) locate(k terminalKind) string {
	if k.app != "" {
		for _, dir := range e.AppDirs {
			app := filepath.Join(dir, k.app)
			if fi, err := os.Stat(filepath.Join(app, "Contents")); err == nil && fi.IsDir() && scriptable(app, k.sdef) {
				return app
			}
		}
		return ""
	}
	if p, err := e.LookPath(k.exe); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	// Installers of kitty, WezTerm and others link it into ~/.local/bin,
	// which a desktop launcher's PATH may lack.
	if e.Home != "" {
		if p := filepath.Join(e.Home, ".local", "bin", k.exe); isExecutable(p) {
			return p
		}
	}
	return ""
}

// scriptable reports whether an app's scripting dictionaries define every
// term, so a version without the commands the script uses is not offered.
func scriptable(app string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	res := filepath.Join(app, "Contents", "Resources")
	entries, err := os.ReadDir(res)
	if err != nil {
		return false
	}
	var sdef []byte
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sdef") {
			if data, err := os.ReadFile(filepath.Join(res, e.Name())); err == nil {
				sdef = append(sdef, data...)
			}
		}
	}
	for _, term := range terms {
		if !bytes.Contains(sdef, []byte(`name="`+term+`"`)) {
			return false
		}
	}
	return true
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// Auto picks the terminal to use when none is chosen: on Linux the one
// $TERMINAL names, then the desktop's own, then the first found; on macOS
// Terminal.
func (e TerminalEnv) Auto(apps []TerminalApp) (TerminalApp, bool) {
	if len(apps) == 0 {
		return TerminalApp{}, false
	}
	if e.GOOS == "linux" {
		var prefs []string
		if t := e.Getenv("TERMINAL"); t != "" {
			prefs = append(prefs, filepath.Base(t))
		}
		prefs = append(prefs, desktopTerminals(e.Getenv("XDG_CURRENT_DESKTOP"))...)
		for _, id := range prefs {
			for _, a := range apps {
				if a.ID == id {
					return a, true
				}
			}
		}
	}
	return apps[0], true
}

// desktopTerminals returns the terminals a desktop ships, newest first, for
// an XDG_CURRENT_DESKTOP value such as "ubuntu:GNOME".
func desktopTerminals(desktops string) []string {
	var ids []string
	for _, d := range strings.Split(strings.ToLower(desktops), ":") {
		switch d {
		case "gnome", "ubuntu", "unity", "budgie", "pantheon", "x-cinnamon":
			ids = append(ids, "ptyxis", "gnome-terminal", "kgx")
		case "kde":
			ids = append(ids, "konsole")
		case "xfce":
			ids = append(ids, "xfce4-terminal")
		}
	}
	return ids
}

// Name returns the display name of a terminal id, or the id itself.
func (e TerminalEnv) Name(id string) string {
	if k, ok := terminalKindFor(e.GOOS, id); ok {
		return k.name
	}
	return id
}

// Resolve returns the terminal for a Settings choice among apps: id, or Auto
// when id is empty.
func (e TerminalEnv) Resolve(apps []TerminalApp, id string) (TerminalApp, error) {
	if id == "" {
		if a, ok := e.Auto(apps); ok {
			return a, nil
		}
		return TerminalApp{}, ErrNoTerminal
	}
	for _, a := range apps {
		if a.ID == id {
			return a, nil
		}
	}
	return TerminalApp{}, &MissingTerminalError{ID: id, Name: e.Name(id)}
}

// OpenIn opens a new window of t that runs cmd in its directory and stays
// open in a login shell after the agent exits. The agent is started by the
// absolute path Find returns.
func OpenIn(t TerminalApp, cmd provider.Command) error {
	if !ChooseTerminal() {
		return ErrUnsupported
	}
	return terminalLauncher{
		goos:      runtime.GOOS,
		find:      Find,
		shell:     loginShell(runtime.GOOS, os.Getenv),
		start:     startDetached,
		osascript: runOSAScript,
	}.open(t, cmd)
}

type terminalLauncher struct {
	goos      string
	find      func(string) (Executable, error)
	shell     string
	start     func(*exec.Cmd) error
	osascript func(app string, script, args []string) error
}

func (l terminalLauncher) open(t TerminalApp, cmd provider.Command) error {
	k, ok := terminalKindFor(l.goos, t.ID)
	if !ok {
		return fmt.Errorf("%s is not a supported terminal", t.Name)
	}
	argv, err := posixArgv(cmd, l.find)
	if err != nil {
		return err
	}
	script := posixScript(argv, cmd.Dir, l.shell)
	if k.app != "" {
		return l.osascript(t.Name, k.script, k.scriptArgs(cmd.Dir, bootstrapLine(script)))
	}
	c := exec.Command(t.Path, k.args(cmd.Dir, []string{"/bin/sh", "-c", script})...)
	c.Dir = cmd.Dir
	return l.start(c)
}

// checkCommand checks what every terminal needs: something to run, and an
// existing directory to run it in. Starting the agent somewhere else would
// resume or hand off in the wrong project.
func checkCommand(cmd provider.Command) error {
	if len(cmd.Argv) == 0 {
		return errors.New("no command to run")
	}
	if cmd.Dir == "" {
		return errors.New("the session has no working directory to open")
	}
	info, err := os.Stat(cmd.Dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("working directory %s does not exist", cmd.Dir)
	}
	return nil
}

// posixArgv checks cmd and returns the agent's argv headed by its absolute
// path. A POSIX shell passes every argument through single quotes unchanged;
// only NUL cannot be passed at all.
func posixArgv(cmd provider.Command, find func(string) (Executable, error)) ([]string, error) {
	if err := checkCommand(cmd); err != nil {
		return nil, err
	}
	exe, err := find(cmd.Argv[0])
	if err != nil {
		return nil, err
	}
	argv := exe.commandLine(exe.Path, cmd.Argv[1:])
	for _, arg := range append([]string{cmd.Dir}, argv...) {
		if strings.ContainsRune(arg, 0) {
			return nil, fmt.Errorf("%w: %q contains a NUL byte", ErrUnsafeArgument, arg)
		}
	}
	return argv, nil
}

// posixScript is the /bin/sh script a terminal runs. It enters dir or stops,
// runs the agent with its own directory first on PATH (so an npm shim's
// `env node` finds the Node it was installed with), reports a failure, and
// then replaces itself with the user's login shell so the window stays.
func posixScript(argv []string, dir, shell string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cd -- %s || { printf 'Press Enter to close. '; read -r _; exit 1; }\n", ShellEscape(dir))
	fmt.Fprintf(&b, "PATH=%s\"${PATH:+:$PATH}\" %s\n", ShellEscape(filepath.Dir(argv[0])), JoinCommand(argv))
	fmt.Fprintf(&b, "s=$?; [ \"$s\" -eq 0 ] || printf '\\n%%s exited with status %%s.\\n' %s \"$s\"\n", ShellEscape(filepath.Base(argv[0])))
	fmt.Fprintf(&b, "exec %s -l\n", ShellEscape(shell))
	return b.String()
}

// loginShell is the user's shell for the window after the agent exits.
func loginShell(goos string, getenv func(string) string) string {
	if s := getenv("SHELL"); filepath.IsAbs(s) && isExecutable(s) {
		return s
	}
	if goos == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// bootstrapLine is the line typed into a macOS terminal's shell, which may
// be zsh, bash, fish or tcsh. It hands script to /bin/sh as printf octal
// escapes, so the line holds no character any of those shells treats
// specially inside single quotes: no quote, backslash pair, %, ! or newline.
func bootstrapLine(script string) string {
	var b strings.Builder
	b.WriteString(`exec /bin/sh -c 'eval "$(printf "$1")"' sh '`)
	for i := 0; i < len(script); i++ {
		c := script[i]
		if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(" /._,:=+@-", c) >= 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, `\%03o`, c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// startDetached starts a terminal in its own session, so it outlives the
// app, and reaps it in the background.
func startDetached(c *exec.Cmd) error {
	detach(c)
	if err := c.Start(); err != nil {
		return fmt.Errorf("could not start %s: %w", filepath.Base(c.Path), err)
	}
	go func() { _ = c.Wait() }()
	return nil
}

// osascriptPath and osascriptWait are variables for tests.
var (
	osascriptPath = "/usr/bin/osascript"
	osascriptWait = 10 * time.Second
)

// runOSAScript runs script with args. The first use asks the user whether
// the app may control the terminal; osascript waits for the answer, so
// after osascriptWait it reports that instead of failing, and is left to
// finish on its own rather than killed or retried.
func runOSAScript(app string, script, args []string) error {
	var argv []string
	for _, line := range script {
		argv = append(argv, "-e", line)
	}
	c := exec.Command(osascriptPath, append(argv, args...)...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return fmt.Errorf("could not run osascript: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return scriptError(app, stderr.String())
		}
		return nil
	case <-time.After(osascriptWait):
		return fmt.Errorf("%s has not opened a window yet. If macOS asks whether Agent Sessions may control %s, allow it", app, app)
	}
}

// scriptError explains an osascript failure by its AppleScript error number.
func scriptError(app, stderr string) error {
	switch {
	case strings.Contains(stderr, "(-1743)"):
		return fmt.Errorf("macOS did not allow Agent Sessions to control %s. Allow it in System Settings > Privacy & Security > Automation, then try again", app)
	case strings.Contains(stderr, "(-1728)"), strings.Contains(stderr, "(-1708)"):
		return fmt.Errorf("this version of %s cannot open a window for Agent Sessions. Update it, or choose another terminal in Settings", app)
	}
	msg := strings.TrimSpace(stderr)
	if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:])
	}
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200]) + "…"
	}
	if msg == "" {
		return fmt.Errorf("%s could not open a window", app)
	}
	return fmt.Errorf("%s could not open a window: %s", app, msg)
}
