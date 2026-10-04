// Package launch turns an agent command (argv plus working directory) into
// something the user can run: a one-line shell command to copy, or, on
// Windows, a console window that runs the agent CLI directly.
package launch

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Format renders cmd as one line for the local shell: PowerShell 5 syntax on
// Windows (where `&&` and POSIX quoting do not work), POSIX sh elsewhere. On
// Windows an agent CLI found only outside PATH, such as one bundled with a
// desktop app, is called by its full path, with its interactive defaults.
func Format(cmd provider.Command) string {
	return formatFor(runtime.GOOS, cmd, Find)
}

func formatFor(goos string, cmd provider.Command, find func(string) (Executable, error)) string {
	if goos != "windows" {
		return PosixCommand(cmd.Argv, cmd.Dir)
	}
	argv := cmd.Argv
	if len(argv) > 0 {
		if exe, err := find(argv[0]); err == nil {
			head := argv[0]
			if !exe.OnPath {
				head = exe.Path
			}
			argv = exe.commandLine(head, argv[1:])
		}
	}
	return PowerShellCommand(argv, cmd.Dir)
}

// commandLine inserts interactive defaults without modifying the caller's
// arguments. An already-prepared argument prefix isn't duplicated.
func (e Executable) commandLine(head string, args []string) []string {
	defaults := e.Args
	if len(args) >= len(defaults) && slices.Equal(args[:len(defaults)], defaults) {
		defaults = nil
	}
	argv := make([]string, 0, 1+len(defaults)+len(args))
	argv = append(argv, head)
	argv = append(argv, defaults...)
	return append(argv, args...)
}

// PosixCommand renders argv for a POSIX shell, changing into dir first:
//
//	cd '<dir>' && claude --resume '<id>'
func PosixCommand(argv []string, dir string) string {
	cmd := JoinCommand(argv)
	if dir == "" {
		return cmd
	}
	return fmt.Sprintf("cd %s && %s", ShellEscape(dir), cmd)
}

// ShellEscape quotes a single argument for POSIX shells. Arguments that are
// safe unquoted are passed through; everything else is single-quoted with
// embedded quotes escaped the POSIX way ('"'"').
func ShellEscape(arg string) string {
	if arg == "" {
		return "''"
	}
	if SafeUnquoted(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}

// SafeUnquoted reports whether arg can appear unquoted in a POSIX shell
// without changing meaning.
func SafeUnquoted(arg string) bool {
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

// JoinCommand builds "argv0 argv1 …" with each argument escaped.
func JoinCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, ShellEscape(arg))
	}
	return strings.Join(parts, " ")
}

// PowerShellCommand renders argv as one line that works in Windows
// PowerShell 5.1 and PowerShell 7. A failed Set-Location stops the line, so
// the agent never starts in the wrong directory. argv[0] is called with the
// call operator unless it is a bare command name.
//
//	Set-Location -LiteralPath 'C:\work dir' -ErrorAction Stop; claude --resume abc
func PowerShellCommand(argv []string, dir string) string {
	var b strings.Builder
	if dir != "" {
		b.WriteString("Set-Location -LiteralPath ")
		b.WriteString(PowerShellQuote(dir))
		b.WriteString(" -ErrorAction Stop; ")
	}
	for i, arg := range argv {
		if i > 0 {
			b.WriteByte(' ')
		} else if !bareCommandName(arg) {
			b.WriteString("& ")
		}
		b.WriteString(PowerShellQuote(arg))
	}
	return b.String()
}

// powerShellQuotes are the characters PowerShell accepts as a single quote.
// Each is doubled inside a single-quoted string.
const powerShellQuotes = "'\u2018\u2019\u201a\u201b"

// PowerShellQuote quotes a single argument for PowerShell. Plain words pass
// through; everything else becomes a verbatim single-quoted string, in
// which PowerShell expands nothing ($, `, @ and , are literal).
func PowerShellQuote(arg string) string {
	if arg != "" && powerShellSafe(arg) {
		return arg
	}
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range arg {
		if strings.ContainsRune(powerShellQuotes, r) {
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

func powerShellSafe(arg string) bool {
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == '\\' || r == ':':
		default:
			return false
		}
	}
	return true
}

// bareCommandName reports whether arg is a plain command name that
// PowerShell resolves through PATH, e.g. "claude".
func bareCommandName(arg string) bool {
	return arg != "" && powerShellSafe(arg) && !strings.ContainsAny(arg, `/\:`) && !strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, ".")
}

// ErrUnsafeArgument means an argument cannot be passed through PowerShell to
// the agent unchanged; copying the command is the fallback.
var ErrUnsafeArgument = errors.New("argument cannot be passed to the agent safely")

// ValidateArgs checks that args reach exe exactly as given when PowerShell
// 5.1 starts it. It mangles empty arguments, embedded double quotes and a
// trailing backslash in an argument it has to quote; npm .cmd shims also run
// through cmd.exe, which expands %VAR% even inside quotes.
func ValidateArgs(exe string, args []string) error {
	ext := strings.ToLower(filepath.Ext(exe))
	shim := ext == ".cmd" || ext == ".bat"
	for _, arg := range args {
		switch {
		case arg == "":
			return fmt.Errorf("%w: empty argument", ErrUnsafeArgument)
		case strings.ContainsAny(arg, "\"\r\n\x00"):
			return fmt.Errorf("%w: %q contains a double quote or line break", ErrUnsafeArgument, arg)
		case strings.ContainsAny(arg, " \t") && strings.HasSuffix(arg, `\`):
			return fmt.Errorf("%w: %q ends with a backslash", ErrUnsafeArgument, arg)
		case shim && strings.Contains(arg, "%"):
			return fmt.Errorf("%w: %q contains %% and %s runs through cmd.exe", ErrUnsafeArgument, arg, filepath.Base(exe))
		}
	}
	return nil
}
