package engine

import (
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

// LiveCodeDiagnosticsInjection is the explicit, reviewed-fixture way to enable
// the live document route with a fake discovery/launcher. Production code never
// passes it; the desktop uses WithLocalLiveCodeDiagnostics instead.
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

// WithLocalLiveCodeDiagnostics enables the production local live route
// (experimental, CD-22): gopls discovered from host metadata and launched only
// after per-project approval. Only the desktop App's local Engine passes it; the
// headless `serve` Engine does not, so remotes stay fail-closed independently
// of the rpc identity gate. A fixture injection takes precedence.
func WithLocalLiveCodeDiagnostics() Option {
	return func(e *Engine) { e.localLive = true }
}

// LiveCodeDiagnosticsReady reports whether this Engine owns a live route: an
// explicit fixture injection, or the local route on a supported platform. It
// does not mean any language server is installed; provider availability is
// reported independently per attachment.
func (e *Engine) LiveCodeDiagnosticsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.liveInjection != nil {
		return e.liveInjection.Discover != nil && e.liveInjection.Session.Launch != nil
	}
	return e.localLive && localLiveSupported()
}

// OwnedCodeDiagnosticsResources reports goroutine counters owned by the
// diagnostics owner (live sessions, document/run workers). Test-visible only;
// it matches no host processes. Zero values when no live route exists.
func (e *Engine) OwnedCodeDiagnosticsResources() (cd.OwnedResources, int64) {
	e.mu.Lock()
	o := e.codeDiagnostics
	e.mu.Unlock()
	if o == nil {
		return cd.OwnedResources{}, 0
	}
	var r cd.OwnedResources
	if o.live != nil {
		r = o.live.Sessions.OwnedResources()
	}
	return r, o.manager.OwnedWorkers()
}
