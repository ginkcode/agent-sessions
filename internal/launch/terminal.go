package launch

import (
	"errors"
	"runtime"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

// ErrUnsupported means this platform cannot open the agent in a terminal
// yet; the command can still be copied.
var ErrUnsupported = errors.New("opening a terminal is not supported on this platform yet; copy the command instead")

// Supported reports whether OpenTerminal works on this platform.
func Supported() bool { return terminalSupported }

// OpenTerminal opens a new terminal window that runs cmd in its directory:
// a Windows PowerShell console on Windows, the automatically chosen terminal
// app elsewhere (OpenIn opens a given one). The agent is started by the
// absolute path Find returns, never through PATH lookups inside the new
// shell. It returns once the window started.
func OpenTerminal(cmd provider.Command) error {
	if !terminalSupported {
		return ErrUnsupported
	}
	if runtime.GOOS != "windows" {
		env := SystemTerminalEnv()
		t, err := env.Resolve(env.Detect(), "")
		if err != nil {
			return err
		}
		return OpenIn(t, cmd)
	}
	script, err := terminalScript(cmd, Find)
	if err != nil {
		return err
	}
	return startTerminal(script, cmd.Dir)
}

// terminalScript checks cmd and renders the PowerShell line the console
// runs.
func terminalScript(cmd provider.Command, find func(string) (Executable, error)) (string, error) {
	if err := checkCommand(cmd); err != nil {
		return "", err
	}
	exe, err := find(cmd.Argv[0])
	if err != nil {
		return "", err
	}
	argv := exe.commandLine(exe.Path, cmd.Argv[1:])
	if err := ValidateArgs(exe.Path, argv[1:]); err != nil {
		return "", err
	}
	return PowerShellCommand(argv, cmd.Dir), nil
}
