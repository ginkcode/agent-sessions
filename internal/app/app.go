// Package app implements the application service and desktop bindings.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/scan"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// App is the desktop application service exposed to the Wails frontend.
type App struct {
	ctx              context.Context
	localEngine      *engine.Engine
	backendMu        sync.RWMutex
	backend          engine.Backend
	svc              *Service
	runner           *scan.Runner
	refresher        *index.Refresher
	manageMu         sync.Mutex
	manage           *manage.Manager
	manageOverride   *manage.Manager
	cacheEnabled     bool
	cacheDirOverride string
	// roots are the provider roots of svc; the engine names its index by them.
	roots              paths.Roots
	saveDialogOverride func(ctx context.Context, defaultName string) (string, error)

	events            *catalogBus
	wailsEvents       engine.Emitter
	conn              *connection
	transportOverride remote.ArtifactTransport
}

// NewApp creates a new App service instance with default providers.
func NewApp() *App {
	roots, _ := paths.Default()
	var provs provider.Set
	if roots.Claude != "" || roots.Codex != "" || roots.OpenCodeData != "" {
		provs = all.Providers(roots, pathutil.NewGitResolver())
	}
	catalog := scan.NewCatalog()
	svc := NewService(catalog, provs)
	runner := &scan.Runner{
		Providers: provs,
		Catalog:   catalog,
	}
	eng := engine.NewEngine(
		engine.WithRoots(roots),
		engine.WithService(svc),
		engine.WithRunner(runner),
		engine.WithCacheEnabled(true),
	)
	return &App{
		localEngine:  eng,
		backend:      eng,
		svc:          svc,
		runner:       runner,
		cacheEnabled: true,
		roots:        roots,
	}
}

// NewAppWithService wires an App onto an explicit Service instance.
func NewAppWithService(svc *Service) *App {
	var runner *scan.Runner
	if svc != nil {
		runner = &scan.Runner{
			Providers: svc.Providers(),
			Catalog:   svc.Catalog(),
		}
	}
	eng := engine.NewEngine(
		engine.WithService(svc),
		engine.WithRunner(runner),
		engine.WithCacheEnabled(false),
	)
	return &App{
		localEngine:  eng,
		backend:      eng,
		svc:          svc,
		runner:       runner,
		cacheEnabled: false,
	}
}

func (a *App) appCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// route snapshots where one binding call goes (see connection.route). Take
// it once per call and use its backend, host and transport together.
func (a *App) route() route {
	var r route
	if a.conn != nil {
		r = a.conn.route()
	}
	if r.backend == nil {
		r.backend = a.localBackend()
	}
	if a.transportOverride != nil {
		r.transport = a.transportOverride
	}
	return r
}

func (a *App) activeBackend() engine.Backend { return a.route().backend }

func (a *App) localBackend() engine.Backend {
	a.backendMu.RLock()
	defer a.backendMu.RUnlock()

	if a.backend != nil {
		if a.manageOverride != nil && a.localEngine != nil {
			a.localEngine.SetManageOverride(a.manageOverride)
		}
		return a.backend
	}
	if a.localEngine != nil {
		if a.manageOverride != nil {
			a.localEngine.SetManageOverride(a.manageOverride)
		}
		return a.localEngine
	}
	a.localEngine = engine.NewEngine(
		engine.WithRoots(a.roots),
		engine.WithService(a.svc),
		engine.WithRunner(a.runner),
		engine.WithCacheEnabled(a.cacheEnabled),
		engine.WithCacheDir(a.cacheDirOverride),
		engine.WithManage(a.manageOverride),
	)
	return a.localEngine
}

// SetArtifactTransportOverride sets an artifact transport override (used in tests).
func (a *App) SetArtifactTransportOverride(t remote.ArtifactTransport) {
	a.transportOverride = t
}

// OnStartup is invoked by Wails when the runtime is ready.
// It starts the local engine and sets up desktop event routing.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx
	wailsEmitter := NewWailsEmitter(ctx)
	a.wailsEvents = wailsEmitter
	a.ensureConn()
	a.conn.setEmitter(wailsEmitter)
	a.events = newCatalogBus(ctx)

	a.localEngine = engine.NewEngine(
		engine.WithRoots(a.roots),
		engine.WithService(a.svc),
		engine.WithRunner(a.runner),
		engine.WithCacheEnabled(a.cacheEnabled),
		engine.WithCacheDir(a.cacheDirOverride),
		engine.WithManage(a.manageOverride),
		engine.WithEmitter(localOnlyEmitter(wailsEmitter, a.conn)),
	)
	a.backendMu.Lock()
	a.backend = a.localEngine
	a.backendMu.Unlock()

	_ = a.localEngine.Start(ctx)
	a.refresher = a.localEngine.Refresher()
	if a.localEngine.Events() != nil {
		a.events = a.localEngine.Events()
	}
}

// Scan triggers a scan across all providers and updates the service state.
func (a *App) Scan() error {
	ctx := a.appCtx()
	if r := a.route(); r.host != "" {
		return r.backend.Scan(ctx)
	}
	if a.localEngine != nil {
		err := a.localEngine.Scan(ctx)
		a.refresher = a.localEngine.Refresher()
		return err
	}
	return nil
}

// OnShutdown is invoked by Wails when the desktop application is terminating.
func (a *App) OnShutdown(ctx context.Context) {
	_ = a.Close()
}

// Close gracefully terminates background scans, watchers, indexers, and the database.
func (a *App) Close() error {
	if a.conn != nil {
		a.conn.Shutdown()
	}
	if a.events != nil {
		a.events.Stop()
	}
	if a.localEngine != nil {
		return a.localEngine.Close()
	}
	return nil
}

// AppVersion returns this desktop app's version, which is "dev" for builds
// without the release ldflags. It is local even while connected to a host.
func (a *App) AppVersion() string {
	return version.Current()
}

// Ping returns a greeting confirmation from the backend service.
func (a *App) Ping(name string) string {
	resp, err := a.activeBackend().Ping(a.appCtx(), name)
	if err != nil {
		return "Hello from agent-sessions backend!"
	}
	return resp
}

// ListGroups returns the grouped navigation tree for the filtered snapshot.
func (a *App) ListGroups(mode GroupMode, filter FilterOpts) ([]GroupNode, error) {
	return a.activeBackend().ListGroups(a.appCtx(), mode, filter)
}

// AgentCounts returns per-agent session totals across the filtered catalog.
func (a *App) AgentCounts(filter FilterOpts) (map[string]int, error) {
	return a.activeBackend().AgentCounts(a.appCtx(), filter)
}

// ListSessions returns session metadata for one group (or all sessions when groupKey is empty).
func (a *App) ListSessions(groupKey string, filter FilterOpts, sort SortOpts) ([]model.SessionMeta, error) {
	return a.activeBackend().ListSessions(a.appCtx(), groupKey, filter, sort)
}

// GetSessionMeta returns the catalog metadata for one session.
func (a *App) GetSessionMeta(ref model.SessionRef) (model.SessionMeta, error) {
	return a.activeBackend().GetSessionMeta(a.appCtx(), ref)
}

// GetMessages returns one page of a session transcript.
func (a *App) GetMessages(ref model.SessionRef, offset int, limit int) (MessagesPage, error) {
	return a.activeBackend().GetMessages(a.appCtx(), ref, offset, limit)
}

// GetBlob returns provider-attached content referenced by a message part.
func (a *App) GetBlob(ref model.SessionRef, key string) (BlobResponse, error) {
	return a.activeBackend().GetBlob(a.appCtx(), ref, key)
}

// CopyResumeCommand builds the provider resume command for a session.
func (a *App) CopyResumeCommand(ref model.SessionRef) (string, error) {
	return a.route().backend.CopyResumeCommand(a.appCtx(), ref)
}

// RevealSource reveals the session's source directory in the system file manager.
func (a *App) RevealSource(ref model.SessionRef) error {
	r := a.route()
	if r.host != "" {
		return errors.New("reveal source is not supported on remote hosts")
	}
	dir, err := r.backend.RevealSource(a.appCtx(), ref)
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", dir)
	} else {
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

// Diagnostics returns aggregated scanner health across providers.
func (a *App) Diagnostics() (provider.Diagnostics, error) {
	return a.activeBackend().Diagnostics(a.appCtx())
}

// OpenURL validates and opens an external HTTP/HTTPS URL in the default browser.
func (a *App) OpenURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported url scheme: %s", scheme)
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", parsed.String())
	} else {
		cmd = exec.Command("xdg-open", parsed.String())
	}
	return cmd.Start()
}
