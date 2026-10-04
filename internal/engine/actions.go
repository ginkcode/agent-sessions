package engine

import (
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// FormatResumeCommand renders a provider resume command as a POSIX shell
// command line, optionally changing into the session's working directory
// first:
//
//	cd "<cwd>" && claude --resume "<id>"
func FormatResumeCommand(cmd provider.Command, cwd string) string {
	if cwd == "" {
		cwd = cmd.Dir
	}
	return launch.PosixCommand(cmd.Argv, cwd)
}

// ShellEscape quotes a single argument for POSIX shells (see launch.ShellEscape).
func ShellEscape(arg string) string { return launch.ShellEscape(arg) }

// SafeUnquoted reports whether arg can appear unquoted in a POSIX shell
// without changing meaning.
func SafeUnquoted(arg string) bool { return launch.SafeUnquoted(arg) }

// JoinCommand builds "argv0 argv1 …" with each argument escaped.
func JoinCommand(argv []string) string { return launch.JoinCommand(argv) }
