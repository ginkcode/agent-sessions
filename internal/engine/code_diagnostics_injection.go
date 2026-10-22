package engine

import (
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

// LiveCodeDiagnosticsInjection is the explicit, reviewed-fixture way to enable
// the live document route on an Engine. Production code never passes it, so the
// default Engine keeps live diagnostics unadvertised and has no launcher.
type LiveCodeDiagnosticsInjection struct {
	// Host overrides the local identity so fixtures can model a verified remote
	// host. Empty uses the Engine's own host identity.
	Host     string
	Discover func(cd.AuthorizedRoot) ([]cd.Registration, error)
	Session  cd.SessionOptions
	// Tick, when non-nil, replaces the event pump ticker so fixtures can drive
	// delivery deterministically (one receive = one pump iteration).
	Tick <-chan time.Time
}

// WithInjectedLiveCodeDiagnostics enables the live route for an explicit fixture.
func WithInjectedLiveCodeDiagnostics(in LiveCodeDiagnosticsInjection) Option {
	return func(e *Engine) { e.liveInjection = &in }
}

// LiveCodeDiagnosticsReady reports whether live diagnostics were explicitly
// injected. It is always false for a production-constructed Engine.
func (e *Engine) LiveCodeDiagnosticsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.liveInjection != nil && e.liveInjection.Discover != nil && e.liveInjection.Session.Launch != nil
}
