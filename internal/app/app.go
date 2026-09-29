// Package app implements the application service and desktop bindings.
package app

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
	"github.com/ginkcode/agent-sessions/internal/scan"
	"github.com/ginkcode/agent-sessions/internal/watch"
)

// App is the desktop application service exposed to the Wails frontend.
type App struct {
	ctx                context.Context
	svc                *Service
	runner             *scan.Runner
	refresher          *index.Refresher
	manageMu           sync.Mutex      // guards manage construction
	manage             *manage.Manager // destructive actions; lazy-built
	manageOverride     *manage.Manager // tests only
	cacheEnabled       bool
	cacheDirOverride   string                                                        // tests only
	saveDialogOverride func(ctx context.Context, defaultName string) (string, error) // tests only

	cancelBootstrap context.CancelFunc // cancels the startup scan
	bootstrapDone   chan struct{}      // closed when the startup scan finishes
	indexer         *index.Indexer     // background FTS indexing worker
	closeWatch      func() error       // stops the filesystem watcher
	events          *catalogBus        // coalesced catalog:changed emitter
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
	return &App{
		svc:          svc,
		runner:       runner,
		cacheEnabled: true,
	}
}

// NewAppWithService wires an App onto an explicit Service instance.
func NewAppWithService(svc *Service) *App {
	var runner *scan.Runner
	if svc != nil {
		runner = &scan.Runner{
			Providers: svc.providers,
			Catalog:   svc.catalog,
		}
	}
	return &App{
		svc:          svc,
		runner:       runner,
		cacheEnabled: false,
	}
}

// OnStartup is invoked by Wails when the runtime is ready.
// It performs the cache-first startup: load persisted metadata, populate the
// catalog, notify the initial UI snapshot — all synchronously and bounded.
// Bootstrap scans run asynchronously, NOT in this paint-critical path.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx
	a.events = newCatalogBus(ctx)

	if !a.cacheEnabled {
		if a.runner != nil {
			go func() {
				_ = a.Scan()
			}()
		}
		return
	}

	if a.svc == nil || len(a.svc.providers) == 0 {
		return
	}

	// Cache-first startup path.
	db, err := index.Open(ctx, a.cacheDir())
	if err != nil {
		// A damaged cache directory or database shows an empty snapshot then
		// rebuilds so the app remains usable.
		if a.svc.catalog != nil {
			a.svc.catalog.Reset(nil)
		}
		if a.runner != nil {
			go func() {
				_ = a.Scan()
			}()
		}
		return
	}

	metas, err := db.LoadCatalog(ctx)
	if err != nil {
		// A damaged cache shows an empty snapshot then rebuilds.
		_ = db.Rebuild(ctx)
		metas = nil
	}

	// Populate the in-memory catalog with cached metas; notify the UI
	// immediately with the initial snapshot.
	if a.svc.catalog != nil {
		a.svc.catalog.Reset(metas)
	}
	a.events.NotifyFullRefresh()

	// Set up the Refresher for cache-backed scans.
	a.refresher = index.NewRefresher(db, a.svc.catalog, a.svc.providers)
	a.refresher.SetOnChanged(func(agent model.AgentID, changed, removed []model.SessionRef) {
		if a.indexer != nil {
			// A scan may have enqueued FTS jobs; wake the background worker.
			a.indexer.Notify()
		}
		a.events.NoteChanged(changed, removed)
	})

	// Background FTS indexing: one worker, notified by scan commits and
	// also polling periodically so restarted jobs resume on their own.
	emitProgress := func(p index.FTSProgress) {
		if a.ctx != nil && a.ctx.Value("events") != nil {
			wruntime.EventsEmit(a.ctx, "index:progress", p)
		}
	}
	a.indexer = index.StartIndexer(ctx, db, a.svc.providers, emitProgress)

	// Filesystem watcher: debounced provider changes trigger an incremental
	// refresh for the changed agent; the refresher's single-flight lock makes
	// overlapping notifications safe.
	a.closeWatch = a.startWatcher(ctx)

	// Bootstrap scans asynchronously, not in the paint-critical path.
	bootCtx, cancel := context.WithCancel(ctx)
	a.cancelBootstrap = cancel
	a.bootstrapDone = make(chan struct{})

	go func() {
		defer close(a.bootstrapDone)
		_ = a.refresher.Bootstrap(bootCtx)
		if a.svc != nil {
			a.svc.ApplyReport(a.refresher.Report())
		}
	}()
}

// Scan triggers a scan across all providers and updates the service state.
// Cache-backed scans notify the UI per committed change via the refresher;
// the uncached runner replaces the catalog wholesale, so it signals a full
// refresh.
func (a *App) Scan() error {
	if a.refresher == nil && a.runner == nil {
		return nil
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if a.refresher != nil {
		err := a.refresher.Bootstrap(ctx)
		if a.svc != nil {
			a.svc.ApplyReport(a.refresher.Report())
		}
		return err
	}
	if a.runner != nil && a.svc != nil {
		report := a.runner.Run(ctx, a.svc.states)
		a.svc.ApplyReport(report)
		a.events.NotifyFullRefresh()
	}
	return nil
}

// OnShutdown is invoked by Wails when the desktop application is terminating.
func (a *App) OnShutdown(ctx context.Context) {
	_ = a.Close()
}

// Close cancels in-flight background scans, waits for workers to complete,
// stops the filesystem watcher, and closes the private cache DB. Catalog
// events stop first so nothing is emitted during or after shutdown.
func (a *App) Close() error {
	a.events.stop()
	if a.closeWatch != nil {
		_ = a.closeWatch()
		a.closeWatch = nil
	}
	if a.cancelBootstrap != nil {
		a.cancelBootstrap()
	}
	if a.bootstrapDone != nil {
		<-a.bootstrapDone
	}
	if a.indexer != nil {
		a.indexer.Close()
	}
	if a.refresher != nil {
		return a.refresher.Close()
	}
	return nil
}

func (a *App) startWatcher(ctx context.Context) func() error {
	if a.svc == nil || len(a.svc.providers) == 0 || a.refresher == nil {
		return nil
	}
	onChange := func(c watch.Change) {
		p, ok := a.svc.providers.Get(c.Agent)
		if !ok {
			return
		}
		go func() {
			scanCtx := a.ctx
			if scanCtx == nil {
				scanCtx = context.Background()
			}
			_, _, _ = a.refresher.Refresh(scanCtx, p)
		}()
	}
	closeWatch, err := watch.Start(ctx, a.svc.providers, onChange)
	if err != nil {
		return nil
	}
	return closeWatch
}

// cacheDir returns the app's private cache directory.
func (a *App) cacheDir() string {
	if a.cacheDirOverride != "" {
		return a.cacheDirOverride
	}
	roots, err := paths.Default()
	if err != nil || roots.Cache == "" {
		return ""
	}
	return roots.Cache
}

// Ping returns a greeting confirmation from the backend service.
func (a *App) Ping(name string) string {
	if name == "" {
		return "Hello from agent-sessions backend!"
	}
	return "Hello " + name + ", welcome to agent-sessions!"
}

// ListGroups returns the grouped navigation tree for the filtered snapshot.
func (a *App) ListGroups(mode GroupMode, filter FilterOpts) ([]GroupNode, error) {
	return a.svc.ListGroups(mode, filter)
}

// AgentCounts returns per-agent session totals across the filtered catalog.
func (a *App) AgentCounts(filter FilterOpts) (map[string]int, error) {
	return a.svc.AgentCounts(filter)
}

// ListSessions returns session metadata for one group (or all sessions when groupKey is empty).
func (a *App) ListSessions(groupKey string, filter FilterOpts, sort SortOpts) ([]model.SessionMeta, error) {
	return a.svc.ListSessions(groupKey, filter, sort)
}

// GetSessionMeta returns the catalog metadata for one session.
func (a *App) GetSessionMeta(ref model.SessionRef) (model.SessionMeta, error) {
	return a.svc.GetSessionMeta(ref)
}

// GetMessages returns one page of a session transcript.
func (a *App) GetMessages(ref model.SessionRef, offset int, limit int) (MessagesPage, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.svc.GetMessages(ctx, ref, offset, limit)
}

// GetBlob returns provider-attached content referenced by a message part.
func (a *App) GetBlob(ref model.SessionRef, key string) (BlobResponse, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.svc.GetBlob(ctx, ref, key)
}

// CopyResumeCommand builds the provider resume command for a session.
func (a *App) CopyResumeCommand(ref model.SessionRef) (string, error) {
	return a.svc.CopyResumeCommand(ref)
}

// RevealSource reveals the session's source directory in the system file manager.
func (a *App) RevealSource(ref model.SessionRef) error {
	dir, err := a.svc.RevealSource(ref)
	if err != nil {
		return err
	}
	cmd := exec.Command("xdg-open", dir)
	return cmd.Start()
}

// Diagnostics returns aggregated scanner health across providers.
func (a *App) Diagnostics() (provider.Diagnostics, error) {
	return a.svc.Diagnostics()
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
	cmd := exec.Command("xdg-open", parsed.String())
	return cmd.Start()
}
