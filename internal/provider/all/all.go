// Package all instantiates all registered providers for the session manager.
package all

import (
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
	"github.com/ginkcode/agent-sessions/internal/provider/codex"
	"github.com/ginkcode/agent-sessions/internal/provider/opencode"
)

// Providers constructs the active provider set.
func Providers(roots paths.Roots, git *pathutil.GitResolver) provider.Set {
	if git == nil {
		git = pathutil.NewGitResolver()
	}
	return provider.Set{
		claude.New(roots.Claude, git),
		codex.New(roots.Codex, git),
		opencode.New(roots.OpenCodeData, git),
	}
}
