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
