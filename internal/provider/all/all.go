// Package all instantiates all registered providers for the session manager.
package all

import (
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
)

// Providers constructs the active provider set. In M0 this includes Claude Code;
// OpenCode and Codex are added in M1.
func Providers(roots paths.Roots, git *pathutil.GitResolver) provider.Set {
	if git == nil {
		git = pathutil.NewGitResolver()
	}
	return provider.Set{
		claude.New(roots.Claude, git),
	}
}
