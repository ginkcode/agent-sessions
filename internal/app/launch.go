package app

import (
	"context"
	"errors"
	"runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// LaunchInfo tells the frontend how sessions can be continued locally.
type LaunchInfo struct {
	// Terminal is true when "Open in terminal" works for local sessions.
	Terminal bool `json:"terminal"`
	// Shell is the syntax of locally copied commands: "powershell" or "posix".
	// Remote commands are always POSIX.
	Shell string `json:"shell"`
}

// ErrTerminalRemote refuses Open in terminal for a remote host's session.
var ErrTerminalRemote = errors.New("open in terminal works for local sessions only; use Copy command and paste it into a shell on the host")

// localLauncher builds agent commands from the local engine.
type localLauncher interface {
	ResumeLaunch(context.Context, model.SessionRef) (provider.Command, error)
	HandoffLaunch(context.Context, engine.HandoffRequest) (provider.Command, error)
	BundleHandoffLaunch(context.Context, engine.BundleHandoffRequest) (provider.Command, error)
}

// LaunchInfo reports the local terminal support and copy-command shell.
func (a *App) LaunchInfo() LaunchInfo {
	info := LaunchInfo{Terminal: a.terminalSupported(), Shell: "posix"}
	if runtime.GOOS == "windows" {
		info.Shell = "powershell"
	}
	return info
}

// OpenResumeInTerminal opens a terminal that resumes a local session.
func (a *App) OpenResumeInTerminal(ref model.SessionRef) error {
	return a.openInTerminal(func(l localLauncher) (provider.Command, error) {
		return l.ResumeLaunch(a.appCtx(), ref)
	})
}

// OpenHandoffInTerminal writes the handoff files and opens a terminal that
// starts the target agent with them.
func (a *App) OpenHandoffInTerminal(req HandoffRequest) error {
	return a.openInTerminal(func(l localLauncher) (provider.Command, error) {
		return l.HandoffLaunch(a.appCtx(), req)
	})
}

// OpenBundleHandoffInTerminal does what OpenHandoffInTerminal does for an
// opened bundle.
func (a *App) OpenBundleHandoffInTerminal(req BundleHandoffRequest) error {
	return a.openInTerminal(func(l localLauncher) (provider.Command, error) {
		return l.BundleHandoffLaunch(a.appCtx(), req)
	})
}

// openInTerminal builds the command on the local engine and opens it. A
// remote host's session is refused rather than started on this machine.
func (a *App) openInTerminal(build func(localLauncher) (provider.Command, error)) error {
	if !a.terminalSupported() {
		return launch.ErrUnsupported
	}
	r := a.route()
	if r.host != "" {
		return ErrTerminalRemote
	}
	l, ok := r.backend.(localLauncher)
	if !ok {
		return launch.ErrUnsupported
	}
	cmd, err := build(l)
	if err != nil {
		return err
	}
	if a.terminalOverride != nil {
		return a.terminalOverride(cmd)
	}
	return launch.OpenTerminal(cmd)
}

func (a *App) terminalSupported() bool {
	return a.terminalOverride != nil || launch.Supported()
}
