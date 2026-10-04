package launch

import (
	"errors"
	"fmt"
	"os"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

// ErrUnsupported means this platform cannot open the agent in a terminal
// yet; the command can still be copied.
var ErrUnsupported = errors.New("opening a terminal is not supported on this platform yet; copy the command instead")

// Supported reports whether OpenTerminal works on this platform.
func Supported() bool { return terminalSupported }

// OpenTerminal opens a new console window that runs cmd in its directory.
// The agent is started by the absolute path Find returns, never through
// PATH lookups inside the new shell. It returns once the window started.
func OpenTerminal(cmd provider.Command) error {
	if !terminalSupported {
		return ErrUnsupported
	}
	script, err := terminalScript(cmd, Find)
	if err != nil {
		return err
	}
	return startTerminal(script, cmd.Dir)
}

// terminalScript checks cmd and renders the PowerShell line the console
// runs. The directory must exist: starting the agent somewhere else would
// resume or hand off in the wrong project.
func terminalScript(cmd provider.Command, find func(string) (Executable, error)) (string, error) {
	if len(cmd.Argv) == 0 {
		return "", errors.New("no command to run")
	}
	if cmd.Dir == "" {
		return "", errors.New("the session has no working directory to open")
	}
	info, err := os.Stat(cmd.Dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("working directory %s does not exist", cmd.Dir)
	}
	exe, err := find(cmd.Argv[0])
	if err != nil {
		return "", err
	}
	if err := ValidateArgs(exe.Path, cmd.Argv[1:]); err != nil {
		return "", err
	}
	return PowerShellCommand(append([]string{exe.Path}, cmd.Argv[1:]...), cmd.Dir), nil
}
