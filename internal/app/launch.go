package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// LaunchInfo tells the frontend how sessions can be continued locally.
type LaunchInfo struct {
	// Terminal is true when "Open in terminal" works for local sessions.
	Terminal bool `json:"terminal"`
	// ChooseTerminal is true where Settings chooses the terminal app.
	ChooseTerminal bool `json:"chooseTerminal"`
	// Shell is the syntax of locally copied commands: "powershell" or "posix".
	// Remote commands are always POSIX.
	Shell string `json:"shell"`
	// WSL is true when Open in terminal also works for a WSL distribution's
	// sessions.
	WSL bool `json:"wsl"`
}

// ErrTerminalRemote refuses Open in terminal for an SSH host's session.
var ErrTerminalRemote = errors.New("open in terminal works for local and WSL sessions only; use Copy command and paste it into a shell on the host")

// localLauncher builds agent commands from the local engine.
type localLauncher interface {
	ResumeLaunch(context.Context, model.SessionRef) (provider.Command, error)
	HandoffLaunch(context.Context, engine.HandoffRequest) (provider.Command, error)
	BundleHandoffLaunch(context.Context, engine.BundleHandoffRequest) (provider.Command, error)
}

// remoteLauncher has a connected host build the script a terminal runs, with
// the agent found on that host.
type remoteLauncher interface {
	ResumeTerminalScript(context.Context, model.SessionRef) (string, error)
	HandoffTerminalScript(context.Context, engine.HandoffRequest) (string, error)
	BundleHandoffTerminalScript(context.Context, engine.BundleHandoffRequest) (string, error)
}

var _ remoteLauncher = (*rpc.Client)(nil)

// LaunchInfo reports the local terminal support and copy-command shell.
func (a *App) LaunchInfo() LaunchInfo {
	info := LaunchInfo{Terminal: a.terminalAvailable(), ChooseTerminal: launch.ChooseTerminal(), Shell: "posix", WSL: a.wslTerminalAvailable()}
	if runtime.GOOS == "windows" {
		info.Shell = "powershell"
	}
	return info
}

func (a *App) wslTerminalAvailable() bool {
	if a.wslTerminalOverride != nil {
		return true
	}
	_, err := launch.WSLBinary()
	return err == nil
}

// OpenResumeInTerminal opens a terminal that resumes a local session.
func (a *App) OpenResumeInTerminal(ref model.SessionRef) error {
	return a.openInTerminal(func(l localLauncher) (provider.Command, error) {
		return l.ResumeLaunch(a.appCtx(), ref)
	}, func(l remoteLauncher) (string, error) {
		return l.ResumeTerminalScript(a.appCtx(), ref)
	})
}

// OpenHandoffInTerminal writes the handoff files and opens a terminal that
// starts the target agent with them.
func (a *App) OpenHandoffInTerminal(req HandoffRequest) error {
	return a.openInTerminal(func(l localLauncher) (provider.Command, error) {
		return l.HandoffLaunch(a.appCtx(), req)
	}, func(l remoteLauncher) (string, error) {
		return l.HandoffTerminalScript(a.appCtx(), req)
	})
}

// OpenBundleHandoffInTerminal does what OpenHandoffInTerminal does for an
// opened bundle.
func (a *App) OpenBundleHandoffInTerminal(req BundleHandoffRequest) error {
	return a.openInTerminal(func(l localLauncher) (provider.Command, error) {
		return l.BundleHandoffLaunch(a.appCtx(), req)
	}, func(l remoteLauncher) (string, error) {
		return l.BundleHandoffTerminalScript(a.appCtx(), req)
	})
}

// openInTerminal builds the command on the local engine and opens it. A WSL
// distribution's session opens in a console on this computer that runs it
// in the distribution; an SSH host's session is refused rather than started
// on this machine.
func (a *App) openInTerminal(build func(localLauncher) (provider.Command, error), script func(remoteLauncher) (string, error)) error {
	r := a.route()
	if distro, ok := remote.ParseWSLTarget(r.host); ok {
		return a.openInWSL(r, distro, script)
	}
	if !a.terminalSupported() {
		return launch.ErrUnsupported
	}
	if r.host != "" {
		return ErrTerminalRemote
	}
	l, ok := r.backend.(localLauncher)
	if !ok {
		return launch.ErrUnsupported
	}
	open, err := a.terminalOpener()
	if err != nil {
		return err
	}
	cmd, err := build(l)
	if err != nil {
		return err
	}
	return open(cmd)
}

// openInWSL has the distribution build the script, so the agent and shell
// are the ones installed there, and opens it in a new console.
func (a *App) openInWSL(r route, distro string, script func(remoteLauncher) (string, error)) error {
	open := a.wslTerminalOverride
	if open == nil {
		if !a.wslTerminalAvailable() {
			return launch.ErrWSLNotInstalled
		}
		open = launch.OpenWSLTerminal
	}
	l, ok := r.backend.(remoteLauncher)
	if !ok {
		return fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
	}
	s, err := script(l)
	if err != nil {
		return err
	}
	return open(distro, s)
}

func (a *App) terminalSupported() bool {
	return a.terminalOverride != nil || launch.Supported()
}
