package claude

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

var _ provider.LiveDetector = (*Provider)(nil)

// Provider implements provider.Provider for Claude Code sessions.
type Provider struct {
	root   string // ~/.claude or custom root
	procFS string // "/proc" on Linux (overridable in tests)
	git    *pathutil.GitResolver
}

// New constructs a Claude Code session provider.
func New(root string, git *pathutil.GitResolver) *Provider {
	if git == nil {
		git = pathutil.NewGitResolver()
	}
	return &Provider{
		root:   filepath.Clean(root),
		procFS: "/proc",
		git:    git,
	}
}

// SetProcFS overrides the procfs mount point for tests.
func (p *Provider) SetProcFS(procFS string) {
	p.procFS = procFS
}

// ID returns the model.AgentClaude identifier.
func (p *Provider) ID() model.AgentID {
	return model.AgentClaude
}

// DisplayName returns human-readable name.
func (p *Provider) DisplayName() string {
	return "Claude Code"
}

// Detect checks if ~/.claude/projects exists and contains any session files.
func (p *Provider) Detect(ctx context.Context) (provider.Detection, error) {
	projDir := filepath.Join(p.root, "projects")
	info, err := os.Stat(projDir)
	if err != nil {
		if os.IsNotExist(err) {
			return provider.Detection{Present: false, Roots: []string{p.root}}, nil
		}
		return provider.Detection{}, err
	}
	if !info.IsDir() {
		return provider.Detection{Present: false, Roots: []string{p.root}}, nil
	}

	sources, err := p.discover(ctx)
	if err != nil {
		return provider.Detection{}, err
	}

	return provider.Detection{
		Present: len(sources) > 0,
		Roots:   []string{p.root},
	}, nil
}

// WatchPaths returns directory paths to monitor for filesystem changes.
func (p *Provider) WatchPaths() []string {
	return []string{
		filepath.Join(p.root, "projects"),
		filepath.Join(p.root, "sessions"),
	}
}

// ResumeCommand generates the command to resume a session in the terminal.
func (p *Provider) ResumeCommand(m model.SessionMeta) provider.Command {
	targetID := m.Ref.ID
	if m.ParentID != "" {
		targetID = m.ParentID
	}
	return provider.Command{
		Argv: []string{"claude", "--resume", targetID},
		Dir:  m.CWD,
	}
}
