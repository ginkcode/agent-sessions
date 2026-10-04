package engine

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// The launch methods return what the copy-command methods format, as argv
// plus directory, so the desktop app can start the agent in a terminal. They
// are local only: not part of Backend or the remote protocol.

// ResumeLaunch returns the provider resume command for a session.
func (e *Engine) ResumeLaunch(_ context.Context, ref model.SessionRef) (provider.Command, error) {
	return e.svc.ResumeLaunch(ref)
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
