package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/term"
)

// Where Resume and "Continue in" open agents: the setting [terminal] open.
const (
	OpenInPanel    = "panel"
	OpenInExternal = "external"
)

// ErrPanelUnavailable means the built-in terminal panel is not running,
// as outside the desktop app.
var ErrPanelUnavailable = errors.New("the built-in terminal is not available")

// terminalManager runs the built-in panel's terminals (term.Manager).
type terminalManager interface {
	Start(term.Spec, term.Tab) (term.Tab, error)
	Write(id string, data []byte) error
	Resize(id string, cols, rows int) error
	Ack(id string, n int)
	Close(id string) error
	CloseAll()
	Running(host string) bool
	Tab(id string) (term.Tab, error)
}

var _ terminalManager = (*term.Manager)(nil)

// panelSystem is what the panel needs from this computer; tests replace it.
type panelSystem struct {
	agentArgv func(provider.Command) ([]string, error)
	shellArgv func() ([]string, error)
	wslBinary func() (string, error)
	// env is the environment of local tabs.
	env  func() []string
	home func() (string, error)
	// dropFiles readies dropped files on a WSL or SSH host (remote.DropFiles).
	dropFiles func(ctx context.Context, host string, opts remote.SSHOptions, paths []string) ([]string, error)
}

func defaultPanelSystem() panelSystem {
	return panelSystem{
		agentArgv: launch.PanelArgv,
		shellArgv: launch.PanelShellArgv,
		wslBinary: launch.WSLBinary,
		env:       term.LoginEnv,
		home:      os.UserHomeDir,
		dropFiles: remote.DropFiles,
	}
}

func (a *App) panelSys() panelSystem {
	if a.panelOverride != nil {
		return *a.panelOverride
	}
	return defaultPanelSystem()
}

// TerminalSize is a terminal's size in character cells.
type TerminalSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// PanelShellRequest opens a plain shell in the panel.
type PanelShellRequest struct {
	// Cwd is the directory to start in, on the selected host; empty or
	// missing means the home directory.
	Cwd string `json:"cwd"`
}

// PanelResume starts a tab in the built-in terminal panel that resumes a
// session, on the host the session belongs to.
func (a *App) PanelResume(ref model.SessionRef, size TerminalSize) (term.Tab, error) {
	tab := term.Tab{Label: "Agent", Kind: term.KindAgent, Agent: string(ref.Agent)}
	return a.startPanelAgent(size, tab, func(l localLauncher) (provider.Command, error) {
		return l.ResumeLaunch(a.appCtx(), ref)
	}, func(l remoteLauncher) (string, error) {
		return l.ResumePanelScript(a.appCtx(), ref)
	})
}

// PanelNewSession starts a panel tab with a fresh session of agent in dir,
// a project of the selected host. The tab is named after the agent and
// records it with the directory, so the session it writes can be found.
func (a *App) PanelNewSession(agent model.AgentID, dir string, size TerminalSize) (term.Tab, error) {
	tab := term.Tab{Label: agentLabel(agent), Kind: term.KindAgent, Agent: string(agent), Dir: dir}
	return a.startPanelAgent(size, tab, func(l localLauncher) (provider.Command, error) {
		return l.NewSessionLaunch(a.appCtx(), agent, dir)
	}, func(l remoteLauncher) (string, error) {
		return l.NewSessionPanelScript(a.appCtx(), agent, dir)
	})
}

// agentLabel names an agent's tab.
func agentLabel(agent model.AgentID) string {
	switch agent {
	case model.AgentClaude:
		return "Claude Code"
	case model.AgentCodex:
		return "Codex"
	case model.AgentOpenCode:
		return "OpenCode"
	}
	return "Agent"
}

// PanelHandoff writes the handoff files and starts a panel tab that runs the
// target agent with them.
func (a *App) PanelHandoff(req HandoffRequest, size TerminalSize) (term.Tab, error) {
	tab := term.Tab{Label: "Agent", Kind: term.KindAgent, Agent: string(req.Target)}
	return a.startPanelAgent(size, tab, func(l localLauncher) (provider.Command, error) {
		return l.HandoffLaunch(a.appCtx(), req)
	}, func(l remoteLauncher) (string, error) {
		return l.HandoffPanelScript(a.appCtx(), req)
	})
}

// PanelBundleHandoff does what PanelHandoff does for an opened bundle.
func (a *App) PanelBundleHandoff(req BundleHandoffRequest, size TerminalSize) (term.Tab, error) {
	tab := term.Tab{Label: "Agent", Kind: term.KindAgent, Agent: string(req.Target)}
	return a.startPanelAgent(size, tab, func(l localLauncher) (provider.Command, error) {
		return l.BundleHandoffLaunch(a.appCtx(), req)
	}, func(l remoteLauncher) (string, error) {
		return l.BundleHandoffPanelScript(a.appCtx(), req)
	})
}

// startPanelAgent routes like openInTerminal, and SSH hosts work too: their
// agent runs over ssh in the tab. Each target is checked before anything is
// built, so a target that cannot start writes no handoff files. tab is the
// tab to show; its host is filled in here.
func (a *App) startPanelAgent(size TerminalSize, tab term.Tab, build func(localLauncher) (provider.Command, error), script func(remoteLauncher) (string, error)) (term.Tab, error) {
	if a.terminals == nil {
		return term.Tab{}, ErrPanelUnavailable
	}
	t := a.launchTarget()
	sys := a.panelSys()
	spec := term.Spec{Cols: size.Cols, Rows: size.Rows}
	tab.Host = t.host
	if t.host == "" {
		l, err := t.local()
		if err != nil {
			return term.Tab{}, err
		}
		cmd, err := build(l)
		if err != nil {
			return term.Tab{}, err
		}
		if spec.Argv, err = sys.agentArgv(cmd); err != nil {
			return term.Tab{}, err
		}
		spec.Dir, spec.Env = cmd.Dir, sys.env()
		tab.Dir = spec.Dir
	} else {
		start, err := a.remotePanel(t, sys)
		if err != nil {
			return term.Tab{}, err
		}
		l, err := t.remote()
		if err != nil {
			return term.Tab{}, err
		}
		s, err := script(l)
		if err != nil {
			return term.Tab{}, err
		}
		start(&spec, s)
	}
	return a.terminals.Start(spec, tab)
}

// PanelShell starts a panel tab with a plain shell on the selected host. A
// WSL distribution or SSH host needs no connection to its server.
func (a *App) PanelShell(req PanelShellRequest, size TerminalSize) (term.Tab, error) {
	if a.terminals == nil {
		return term.Tab{}, ErrPanelUnavailable
	}
	t := a.launchTarget()
	sys := a.panelSys()
	spec := term.Spec{Cols: size.Cols, Rows: size.Rows}
	if t.host == "" {
		argv, err := sys.shellArgv()
		if err != nil {
			return term.Tab{}, err
		}
		spec.Argv, spec.Env = argv, sys.env()
		if info, err := os.Stat(req.Cwd); req.Cwd != "" && err == nil && info.IsDir() {
			spec.Dir = req.Cwd
		} else if home, err := sys.home(); err == nil {
			spec.Dir = home
		}
	} else {
		start, err := a.remotePanel(t, sys)
		if err != nil {
			return term.Tab{}, err
		}
		start(&spec, launch.PanelShellScript(req.Cwd))
	}
	// Here spec.Dir is where the shell starts; elsewhere it is the directory
	// asked for, which the shell falls back from to the home directory.
	dir := spec.Dir
	if t.host != "" {
		dir = req.Cwd
	}
	return a.terminals.Start(spec, term.Tab{Label: "Shell", Host: t.host, Kind: term.KindShell, Dir: dir})
}

// remotePanel checks that a WSL distribution or SSH host's tab can start,
// and returns what fills in its spec for the /bin/sh script to run there.
func (a *App) remotePanel(t launchTarget, sys panelSystem) (func(spec *term.Spec, script string), error) {
	if t.distro != "" {
		wsl, err := sys.wslBinary()
		if err != nil {
			return nil, err
		}
		home, _ := sys.home()
		return func(spec *term.Spec, script string) {
			spec.Argv = append([]string{wsl}, launch.WSLExecArgs(t.distro, script)...)
			spec.Dir = home
		}, nil
	}
	opts := remote.SSHOptions{}
	if a.conn != nil {
		opts = a.conn.sshOptions()
	}
	opts.NoTTY, opts.ForceTTY, opts.Interactive = false, true, true
	argv, env, err := remote.SSHCommandLine(t.host, nil, opts)
	if err != nil {
		return nil, err
	}
	return func(spec *term.Spec, script string) {
		spec.Argv = append(argv, remote.QuoteArgs(launch.PosixBootstrapArgv(script)))
		spec.Env = env
	}, nil
}

// TerminalWrite sends typed input to a panel tab.
func (a *App) TerminalWrite(id, data string) error {
	if a.terminals == nil {
		return ErrPanelUnavailable
	}
	return a.terminals.Write(id, []byte(data))
}

// TerminalResize sets a panel tab's size in cells.
func (a *App) TerminalResize(id string, cols, rows int) error {
	if a.terminals == nil {
		return ErrPanelUnavailable
	}
	return a.terminals.Resize(id, cols, rows)
}

// TerminalAck acknowledges n bytes of a tab's output the panel has shown.
func (a *App) TerminalAck(id string, n int) {
	if a.terminals != nil {
		a.terminals.Ack(id, n)
	}
}

// TerminalClose ends a panel tab's process.
func (a *App) TerminalClose(id string) error {
	if a.terminals == nil {
		return nil
	}
	return a.terminals.Close(id)
}

// dropTimeout bounds copying dropped files to an SSH host.
const dropTimeout = 5 * time.Minute

// TerminalDropFiles readies files dropped on a panel tab for its process
// and returns their paths as typed at its prompt. A WSL tab gets the
// distribution's paths for them, and an SSH tab the paths of copies made
// on its host.
func (a *App) TerminalDropFiles(id string, paths []string) ([]string, error) {
	if a.terminals == nil {
		return nil, ErrPanelUnavailable
	}
	tab, err := a.terminals.Tab(id)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	windows := tab.Host == "" && runtime.GOOS == "windows"
	if tab.Host != "" {
		opts := remote.SSHOptions{}
		if a.conn != nil {
			opts = a.conn.sshOptions()
		}
		ctx, cancel := context.WithTimeout(a.appCtx(), dropTimeout)
		defer cancel()
		if paths, err = a.panelSys().dropFiles(ctx, tab.Host, opts, paths); err != nil {
			return nil, err
		}
	}
	typed := make([]string, len(paths))
	for i, p := range paths {
		typed[i] = typedPath(p, windows)
	}
	return typed, nil
}

// typedPath quotes a path the way terminal apps type a dropped file, which
// agents recognize: in double quotes on Windows, else in single quotes,
// and only when it has characters a shell would split or expand.
func typedPath(p string, windows bool) string {
	safe := `-_./:+,@%=`
	if windows {
		safe = `-_./:\`
	}
	plain := p != "" && strings.IndexFunc(p, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(safe, r))
	}) < 0
	switch {
	case plain:
		return p
	case windows:
		return `"` + p + `"`
	default:
		return remote.QuotePOSIX(p)
	}
}

// SetTerminalOpen chooses where Resume and "Continue in" open agents:
// OpenInPanel or OpenInExternal.
func (a *App) SetTerminalOpen(mode string) (LaunchInfo, error) {
	if mode != OpenInPanel && mode != OpenInExternal {
		return LaunchInfo{}, fmt.Errorf("unknown terminal mode %q", mode)
	}
	path, err := a.localConfigPath()
	if err != nil {
		return LaunchInfo{}, err
	}
	err = configfile.Update(path, func(data []byte) []byte {
		return configfile.SetValue(data, "terminal", "open", strconv.Quote(mode))
	})
	if err != nil {
		return LaunchInfo{}, fmt.Errorf("save terminal: %w", err)
	}
	return a.LaunchInfo(), nil
}

// terminalOpen is where agents open: the saved choice while the panel is
// available, else the external terminal.
func (a *App) terminalOpen() string {
	if a.terminals == nil {
		return OpenInExternal
	}
	path, err := a.localConfigPath()
	if err != nil {
		return OpenInPanel
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return OpenInPanel
	}
	if mode, _ := configfile.Value(data, "terminal", "open"); mode == OpenInExternal {
		return OpenInExternal
	}
	return OpenInPanel
}
