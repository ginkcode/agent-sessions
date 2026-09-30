package remote

import (
	"strings"
)

// QuotePOSIX quotes a string for safe evaluation in a POSIX shell (/bin/sh, bash, zsh).
// It wraps the string in single quotes and replaces embedded single quotes with '\''.
// Empty string returns "''".
func QuotePOSIX(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteArgs quotes a list of arguments joined by single spaces.
func QuoteArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = QuotePOSIX(a)
	}
	return strings.Join(quoted, " ")
}

// WrapSSHCommand formats a command to execute on a remote host via interactive ssh (`ssh -t <alias> '<cmd>'`).
// If alias or cmd is empty, the original command is returned unchanged.
func WrapSSHCommand(alias, cmd string) string {
	if alias == "" || cmd == "" {
		return cmd
	}
	return "ssh -t " + alias + " " + QuotePOSIX(cmd)
}

