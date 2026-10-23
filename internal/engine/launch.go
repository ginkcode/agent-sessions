package engine

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// The launch methods return what the copy-command methods format, as argv
// plus directory, so the desktop app can start the agent in a terminal. They
// are not part of Backend: a remote server renders their command into a
// terminal script instead (rpc's *TerminalScript methods).

// ResumeLaunch returns the provider resume command for a session.
func (e *Engine) ResumeLaunch(_ context.Context, ref model.SessionRef) (provider.Command, error) {
	return e.svc.ResumeLaunch(ref)
}

// NewSessionLaunch returns the command that starts a fresh session of agent
// in dir.
func (e *Engine) NewSessionLaunch(_ context.Context, agent model.AgentID, dir string) (provider.Command, error) {
	return e.svc.NewSessionLaunch(agent, dir)
}

// NewSessionAgents reports which agents' CLIs are installed on this machine.
func (e *Engine) NewSessionAgents(_ context.Context) ([]AgentStatus, error) {
	return e.svc.NewSessionAgents(), nil
}

// HandoffLaunch writes the handoff files and returns the target agent's command.
func (e *Engine) HandoffLaunch(ctx context.Context, req HandoffRequest) (provider.Command, error) {
	return e.svc.HandoffLaunch(ctx, req)
}

// BundleHandoffLaunch writes the bundle handoff files and returns the target
// agent's command.
func (e *Engine) BundleHandoffLaunch(ctx context.Context, req BundleHandoffRequest) (provider.Command, error) {
	return e.svc.BundleHandoffLaunch(ctx, req)
}
