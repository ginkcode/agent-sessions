// Package app implements the application service and desktop bindings.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/scan"
	"github.com/ginkcode/agent-sessions/internal/term"
	"github.com/ginkcode/agent-sessions/internal/translate"
	"github.com/ginkcode/agent-sessions/internal/version"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
	// localLive enables the local live code-diagnostics route (desktop only).
	localLive bool
	// roots are the provider roots of svc; the engine names its index by them.
	roots                  paths.Roots
	saveDialogOverride     func(ctx context.Context, defaultName string) (string, error)
	openFileDialogOverride func(ctx context.Context) (string, error)
	// terminalOverride replaces launch.OpenTerminal in tests.
	terminalOverride func(provider.Command) error
	// terminalEnv and openInOverride replace terminal detection and
	// launch.OpenIn in tests.
	terminalEnv    *launch.TerminalEnv
	openInOverride func(launch.TerminalApp, provider.Command) error
	// wslTerminalOverride replaces launch.OpenWSLTerminal in tests.
	wslTerminalOverride func(distro, script string) error
	// terminals runs the built-in terminal panel's tabs; nil outside the
	// desktop app. panelOverride replaces what the panel needs from this
	// computer in tests.
	terminals     terminalManager
	panelOverride *panelSystem
	// translator replaces the default translation client in tests.
	translator *translate.Client

	events            *catalogBus
	wailsEvents       engine.Emitter
	conn              *connection
	transportOverride remote.ArtifactTransport
	fileClose         fileCloseGuard
	searches          fileSearches
	gitSyncs          fileSearches // fetch/pull/push in progress, by sync ID

	// Agent alerts: notify is set up on first use; notifierOverride and
	// notifyRaise replace the desktop's notifications and raising the
	// window in tests.
	notifyOnce       sync.Once
	notify           *notifications
	notifierOverride notifier
	notifyRaise      func()
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
		engine.WithLocalLiveCodeDiagnostics(),
	)
	return &App{
		localEngine:  eng,
		backend:      eng,
		svc:          svc,
		runner:       runner,
		cacheEnabled: true,
		localLive:    true,
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
		localLiveOption(a.localLive),
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
	if a.terminals == nil {
		a.terminals = term.NewManager(wailsEmitter)
	}
	a.conn.setBusy(a.terminals.Running)
	if dir := a.claimsDir(); dir != "" {
		claims := newHostClaims(dir, func() { raiseWindow(ctx) })
		a.conn.setClaims(claims)
		// Without a place in the ring this window is just skipped by the
		// window shortcut; everything else works.
		_ = claims.joinRing()
	}
	a.events = newCatalogBus(ctx)

	a.localEngine = engine.NewEngine(
		engine.WithRoots(a.roots),
		engine.WithService(a.svc),
		engine.WithRunner(a.runner),
		engine.WithCacheEnabled(a.cacheEnabled),
		engine.WithCacheDir(a.cacheDirOverride),
		engine.WithManage(a.manageOverride),
		localLiveOption(a.localLive),
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

// newWindowCommand starts another copy of this app; tests replace it.
var newWindowCommand = func() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the app: %w", err)
	}
	return exec.Command(exe), nil
}

// NewWindow opens another window of the app. Each window is its own
// process with its own connection, so windows can show different hosts;
// a host is open in one window at a time (see hostClaims). On macOS this
// also works where opening the app again would only focus this window.
func (a *App) NewWindow() error {
	cmd, err := newWindowCommand()
	if err != nil {
		return err
	}
	// The new window must not inherit this one's askpass session.
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AGENT_SESSIONS_ASKPASS_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	if err := startDetached(cmd); err != nil {
		return fmt.Errorf("open a new window: %w", err)
	}
	return nil
}

// FocusNextWindow brings the app's next open window to the front, or the
// previous one when forward is false, in the order they were opened. With
// one window open it does nothing.
func (a *App) FocusNextWindow(forward bool) error {
	var claims *hostClaims
	if a.conn != nil {
		a.conn.mu.Lock()
		claims = a.conn.claims
		a.conn.mu.Unlock()
	}
	if claims == nil {
		return nil
	}
	if err := claims.focusNextWindow(forward); err != nil {
		return fmt.Errorf("switch window: %w", err)
	}
	return nil
}

// claimsDir holds the claims that keep each host open in one window at
// most; empty without a cache dir.
func (a *App) claimsDir() string {
	dir := a.cacheDirOverride
	if dir == "" {
		dir = a.roots.Cache
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "windows")
}

// raiseWindow brings this app window to the front, restoring it when
// minimized. Each platform needs a different call: on Linux only
// WindowUnminimise presents the window, and on Windows it would also
// restore a maximized window to its normal size.
func raiseWindow(ctx context.Context) {
	switch runtime.GOOS {
	case "linux":
		wruntime.WindowUnminimise(ctx)
	case "darwin":
		if wruntime.WindowIsMinimised(ctx) {
			wruntime.WindowUnminimise(ctx)
		}
		wruntime.WindowShow(ctx)
	default:
		wruntime.WindowShow(ctx)
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
	a.closeNotifications()
	_ = a.Close()
}

// Close gracefully terminates background scans, watchers, indexers, and the database.
func (a *App) Close() error {
	// Terminals end first: an SSH tab shares the connection's ssh master.
	if a.terminals != nil {
		a.terminals.CloseAll()
	}
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
	return startDetached(desktopOpen(runtime.GOOS, dir, false))
}

// ListFiles lists the files of a session directory on the selected host,
// for the read-only file viewer.
func (a *App) ListFiles(dir string) (files.Listing, error) {
	return a.activeBackend().ListFiles(a.appCtx(), dir)
}

// BlameFile returns blame for one saved line on the selected host.
func (a *App) BlameFile(dir, path string, line int) (files.BlameLine, error) {
	return a.activeBackend().BlameFile(a.appCtx(), dir, path, line)
}

// ProbeFiles observes saved paths on one captured host route. Consumers must
// also discard replies from superseded connection/request epochs.
func (a *App) ProbeFiles(dir string, paths []string, host string) (files.ProbeResult, error) {
	r := a.route()
	if r.host != host {
		return files.ProbeResult{}, errors.New("file probe host changed")
	}
	out, err := r.backend.ProbeFiles(a.appCtx(), dir, paths)
	current := a.route()
	if current.host != r.host || current.backend != r.backend || current.generation != r.generation {
		return files.ProbeResult{}, errors.New("file probe host changed")
	}
	return out, err
}

// ReadFile reads one file of a session directory on the selected host.
func (a *App) ReadFile(dir, path string) (files.Content, error) {
	return a.activeBackend().ReadFile(a.appCtx(), dir, path)
}

// SaveFile saves a file edited in the file viewer on the selected host, if
// it still has the revision it was edited from. host is the host the editor
// read the file from ("" for Local); the save is refused, with nothing
// written, when calls are routed elsewhere by now. The host is checked
// against the same routing snapshot the save uses, so a host switch cannot
// slip in between.
func (a *App) SaveFile(dir string, req files.SaveRequest, host string) (files.SaveResult, error) {
	r := a.route()
	if r.host != host {
		return files.SaveResult{}, ErrSaveHostChanged
	}
	return r.backend.SaveFile(a.appCtx(), dir, req)
}

// ReplaceInFiles replaces search matches in files on the selected host.
// host is the host the search ran on ("" for Local); as with SaveFile,
// nothing is written when calls are routed elsewhere by now.
func (a *App) ReplaceInFiles(dir string, req files.ReplaceRequest, host string) (files.ReplaceResult, error) {
	r := a.route()
	if r.host != host {
		return files.ReplaceResult{}, ErrSaveHostChanged
	}
	return r.backend.ReplaceInFiles(a.appCtx(), dir, req)
}

// ErrSaveHostChanged refuses a save meant for another host than the one
// calls now go to. Nothing was written.
var ErrSaveHostChanged = errors.New("the connection changed to another host; nothing was saved")

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
	return startDetached(desktopOpen(runtime.GOOS, parsed.String(), true))
}

// desktopOpen returns the command that opens target, a local directory or an
// http(s) URL, with the desktop's default handler on goos. On Windows a URL
// goes to url.dll rather than through cmd.exe, which would interpret & and ^.
func desktopOpen(goos, target string, isURL bool) *exec.Cmd {
	switch {
	case goos == "darwin":
		return exec.Command("open", target)
	case goos == "windows" && isURL:
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case goos == "windows":
		return exec.Command("explorer", target)
	}
	return exec.Command("xdg-open", target)
}

// startDetached starts cmd and reaps it in the background. Explorer exits 1
// even on success, so the exit status is ignored.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// localLiveOption enables the local live diagnostics route only for the
// desktop App (NewApp); explicit-service/test Apps keep the checker-only route.
func localLiveOption(enabled bool) engine.Option {
	if enabled {
		return engine.WithLocalLiveCodeDiagnostics()
	}
	return func(*engine.Engine) {}
}
