package engine

import (
	"context"
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
// (experimental, CD-22): language servers discovered from host metadata and
// launched only after per-project approval. The desktop App's local Engine
// passes it; a headless `serve` Engine gets it through
// WithRemoteCodeDiagnostics. A fixture injection takes precedence.
func WithLocalLiveCodeDiagnostics() Option {
	return func(e *Engine) { e.localLive = true }
}

// WithRemoteCodeDiagnostics makes a headless `serve` Engine the code
// diagnostics host for an SSH or WSL connection: the identity this host
// computes gets that kind, and the live route runs here, on the remote host.
// kind comes from the launcher's command line environment, never from a
// request; anything other than "ssh" or "wsl" leaves diagnostics unsupported.
func WithRemoteCodeDiagnostics(kind string) Option {
	return func(e *Engine) {
		if kind == "ssh" || kind == "wsl" {
			e.remoteKind, e.localLive = kind, true
		}
	}
}

func (e *Engine) hostKind() string {
	if e.remoteKind != "" {
		return e.remoteKind
	}
	return "local"
}

// RemoteCodeDiagnosticsHostID is the identity rpc initialize advertises. Only
// an Engine composed with WithRemoteCodeDiagnostics has one.
func (e *Engine) RemoteCodeDiagnosticsHostID(ctx context.Context) (string, error) {
	if e.remoteKind == "" {
		return "", diagnosticError("unsupported", "Not a remote diagnostics host")
	}
	return e.CodeDiagnosticsHostID(ctx)
}

// WithFixtureMachineIdentity replaces platform machine-identity discovery with a
// fixed value so fixtures get a deterministic local host identity on every OS.
// The account and installation components are still derived as in production,
// and an empty id fails closed exactly like an unavailable platform identity.
// Production composition never passes it.
func WithFixtureMachineIdentity(id string) Option {
	return func(e *Engine) {
		e.machineID = func() (string, error) { return id, nil }
	}
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
