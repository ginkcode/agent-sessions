package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func formatResumeCommand(cmd provider.Command, cwd string) string {
	return engine.FormatResumeCommand(cmd, cwd)
}

func shellEscape(arg string) string {
	return engine.ShellEscape(arg)
}

func safeUnquoted(arg string) bool {
	return engine.SafeUnquoted(arg)
}

func joinCommand(argv []string) string {
	return engine.JoinCommand(argv)
}
