package app

import (
	"fmt"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

// formatResumeCommand renders a provider resume command as a shell command
// line, optionally changing into the session's working directory first:
//
//	cd "<cwd>" && claude --resume "<id>"
func formatResumeCommand(cmd provider.Command, cwd string) string {
	argv := cmd.Argv
	if cmd.Dir != "" && cwd == "" {
		cwd = cmd.Dir
	}
	cmdStr := joinCommand(argv)
	if cwd == "" {
		return cmdStr
	}
	return fmt.Sprintf("cd %s && %s", shellEscape(cwd), cmdStr)
}

// shellEscape quotes a single argument for POSIX shells. Arguments that are
// safe unquoted are passed through; everything else is single-quoted with
// embedded quotes escaped the POSIX way ('"'"').
func shellEscape(arg string) string {
	if arg == "" {
		return "''"
	}
	if safeUnquoted(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}

// safeUnquoted reports whether arg can appear unquoted in a POSIX shell
// without changing meaning.
func safeUnquoted(arg string) bool {
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '@' || r == '+' || r == ',':
		default:
			return false
		}
	}
	return true
}

// joinCommand builds "argv0 argv1 …" with each argument escaped.
func joinCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellEscape(arg))
	}
	return strings.Join(parts, " ")
}