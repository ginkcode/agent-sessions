package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

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

var _ Backend = (*Engine)(nil)

// Engine is the central headless application engine. It owns the service,
// scanner, indexer, watcher, destructive action manager, and catalog event bus.
// It has zero GUI or Wails dependencies and implements Backend.
type Engine struct {
	mu sync.Mutex

	svc              *Service
	runner           *scan.Runner
	refresher        *index.Refresher
	indexer          *index.Indexer
	closeWatch       func() error
	events           *CatalogBus
	emitter          Emitter

	manageMu         sync.Mutex
	manage           *manage.Manager
	manageOverride   *manage.Manager

	cacheEnabled     bool
	cacheDirOverride string
	roots            paths.Roots

	cancelBootstrap  context.CancelFunc
	bootstrapDone    chan struct{}
	started          bool
	closed           bool
}

// Option configures an Engine instance.
type Option func(*Engine)

// WithEmitter sets the event emitter for catalog and indexer notifications.
func WithEmitter(emitter Emitter) Option {
	return func(e *Engine) {
		e.emitter = emitter
	}
}

// WithCacheEnabled toggles sqlite-backed caching on or off.
func WithCacheEnabled(enabled bool) Option {
	return func(e *Engine) {
		e.cacheEnabled = enabled
	}
}

// WithCacheDir overrides the cache directory used by the engine.
func WithCacheDir(dir string) Option {
	return func(e *Engine) {
		e.cacheDirOverride = dir
	}
}

// WithRoots configures explicit filesystem roots.
func WithRoots(roots paths.Roots) Option {
	return func(e *Engine) {
		e.roots = roots
	}
}

// WithService wires an explicit Service instance.
func WithService(svc *Service) Option {
	return func(e *Engine) {
		e.svc = svc
	}
}

// WithRunner wires an explicit scan runner.
func WithRunner(runner *scan.Runner) Option {
	return func(e *Engine) {
		e.runner = runner
	}
}

// WithManage overrides the manage manager (useful in tests).
func WithManage(mgr *manage.Manager) Option {
	return func(e *Engine) {
		e.manageOverride = mgr
	}
}

// NewEngine creates an Engine instance with default options.
func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		cacheEnabled: true,
		emitter:      NopEmitter{},
	}
	for _, opt := range opts {
		opt(e)
	}

	if e.svc == nil {
		roots := e.roots
		if roots.Claude == "" && roots.Codex == "" && roots.OpenCodeData == "" {
			roots, _ = paths.Default()
			e.roots = roots
		}
		var provs provider.Set
		if roots.Claude != "" || roots.Codex != "" || roots.OpenCodeData != "" {
			provs = all.Providers(roots, pathutil.NewGitResolver())
		}
		catalog := scan.NewCatalog()
		e.svc = NewService(catalog, provs)
		if e.runner == nil {
			e.runner = &scan.Runner{
				Providers: provs,
				Catalog:   catalog,
			}
		}
	} else if e.runner == nil {
		e.runner = &scan.Runner{
			Providers: e.svc.Providers(),
			Catalog:   e.svc.Catalog(),
		}
	}

	return e
}

// Service returns the underlying Service instance.
func (e *Engine) Service() *Service {
	return e.svc
}

// Refresher returns the underlying Refresher instance, if started.
func (e *Engine) Refresher() *index.Refresher {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.refresher
}

// Events returns the catalog bus.
func (e *Engine) Events() *CatalogBus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.events
}

// SetEmitter updates the event emitter.
func (e *Engine) SetEmitter(emitter Emitter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if emitter == nil {
		emitter = NopEmitter{}
	}
	e.emitter = emitter
	if e.events != nil {
		e.events.emitter = emitter
	}
}

// Start boots the engine's cache, catalog, indexer, watcher, and bootstrap scan.
// It is idempotent and safe to call multiple times.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.started {
		return nil
	}
	e.started = true
	e.closed = false

	if e.emitter == nil {
		e.emitter = NopEmitter{}
	}
	e.events = NewCatalogBus(e.emitter)

	if !e.cacheEnabled {
		if e.runner != nil {
			go func() {
				_ = e.Scan(ctx)
			}()
		}
		return nil
	}

	if e.svc == nil || len(e.svc.Providers()) == 0 {
		return nil
	}

	db, err := index.Open(ctx, e.cacheDir())
	if err != nil {
		if e.svc.Catalog() != nil {
			e.svc.Catalog().Reset(nil)
		}
		if e.runner != nil {
			go func() {
				_ = e.Scan(ctx)
			}()
		}
		return nil
	}

	metas, err := db.LoadCatalog(ctx)
	if err != nil {
		_ = db.Rebuild(ctx)
		metas = nil
	}

	if e.svc.Catalog() != nil {
		e.svc.Catalog().Reset(metas)
	}
	e.events.NotifyFullRefresh()

	e.refresher = index.NewRefresher(db, e.svc.Catalog(), e.svc.Providers())
	e.refresher.SetOnChanged(func(agent model.AgentID, changed, removed []model.SessionRef) {
		e.mu.Lock()
		indexer := e.indexer
		events := e.events
		e.mu.Unlock()
		if indexer != nil {
			indexer.Notify()
		}
		if events != nil {
			events.NoteChanged(changed, removed)
		}
	})

	emitProgress := func(p index.FTSProgress) {
		e.mu.Lock()
		emitter := e.emitter
		e.mu.Unlock()
		if emitter != nil {
			emitter.Emit("index:progress", p)
		}
	}
	e.indexer = index.StartIndexer(ctx, db, e.svc.Providers(), emitProgress)

	e.closeWatch = e.startWatcher(ctx)

	bootCtx, cancel := context.WithCancel(ctx)
	e.cancelBootstrap = cancel
	done := make(chan struct{})
	e.bootstrapDone = done
	ref := e.refresher

	go func() {
		defer close(done)
		if ref != nil {
			_ = ref.Bootstrap(bootCtx)
			if e.svc != nil {
				e.svc.ApplyReport(ref.Report())
			}
		}
	}()

	return nil
}

func (e *Engine) startWatcher(ctx context.Context) func() error {
	if e.svc == nil || len(e.svc.Providers()) == 0 || e.refresher == nil {
		return nil
	}
	onChange := func(c watch.Change) {
		p, ok := e.svc.Providers().Get(c.Agent)
		if !ok {
			return
		}
		go func() {
			scanCtx := ctx
			if scanCtx == nil {
				scanCtx = context.Background()
			}
			_, _, _ = e.refresher.Refresh(scanCtx, p)
			if e.svc != nil {
				e.svc.ApplyReport(e.refresher.Report())
			}
		}()
	}
	closeWatch, err := watch.Start(ctx, e.svc.Providers(), onChange)
	if err != nil {
		return nil
	}
	return closeWatch
}

// Scan triggers a scan across all providers.
func (e *Engine) Scan(ctx context.Context) error {
	e.mu.Lock()
	refresher := e.refresher
	runner := e.runner
	svc := e.svc
	events := e.events
	e.mu.Unlock()

	if refresher == nil && runner == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if refresher != nil {
		err := refresher.Bootstrap(ctx)
		if svc != nil {
			svc.ApplyReport(refresher.Report())
		}
		return err
	}
	if runner != nil && svc != nil {
		report := runner.Run(ctx, svc.ScanStates())
		svc.ApplyReport(report)
		if events != nil {
			events.NotifyFullRefresh()
		}
	}
	return nil
}

// Close gracefully terminates background scans, watchers, indexers, and the database.
// It is idempotent and uses an explicit shutdown order.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.started = false

	events := e.events
	closeWatch := e.closeWatch
	cancelBootstrap := e.cancelBootstrap
	bootstrapDone := e.bootstrapDone
	indexer := e.indexer
	refresher := e.refresher

	e.events = nil
	e.closeWatch = nil
	e.cancelBootstrap = nil
	e.bootstrapDone = nil
	e.indexer = nil
	e.refresher = nil
	e.mu.Unlock()

	if events != nil {
		events.Stop()
	}
	if closeWatch != nil {
		_ = closeWatch()
	}
	if cancelBootstrap != nil {
		cancelBootstrap()
	}
	if bootstrapDone != nil {
		<-bootstrapDone
	}
	if indexer != nil {
		indexer.Close()
	}
	if refresher != nil {
		return refresher.Close()
	}
	return nil
}

func (e *Engine) cacheDir() string {
	if e.cacheDirOverride != "" {
		return e.cacheDirOverride
	}
	if e.roots.Cache != "" {
		return e.roots.Cache
	}
	roots, err := paths.Default()
	if err != nil || roots.Cache == "" {
		return ""
	}
	return roots.Cache
}

// Ping returns a greeting confirmation from the backend service.
func (e *Engine) Ping(_ context.Context, name string) (string, error) {
	if name == "" {
		return "Hello from agent-sessions backend!", nil
	}
	return "Hello " + name + ", welcome to agent-sessions!", nil
}

// ListGroups returns the grouped navigation tree for the filtered snapshot.
func (e *Engine) ListGroups(_ context.Context, mode GroupMode, filter FilterOpts) ([]GroupNode, error) {
	return e.svc.ListGroups(mode, filter)
}

// ListSessions returns session metadata for one group.
func (e *Engine) ListSessions(_ context.Context, groupKey string, filter FilterOpts, sort SortOpts) ([]model.SessionMeta, error) {
	return e.svc.ListSessions(groupKey, filter, sort)
}

// AgentCounts returns per-agent session totals across the filtered catalog.
func (e *Engine) AgentCounts(_ context.Context, filter FilterOpts) (map[string]int, error) {
	return e.svc.AgentCounts(filter)
}

// GetSessionMeta returns the catalog metadata for one session.
func (e *Engine) GetSessionMeta(_ context.Context, ref model.SessionRef) (model.SessionMeta, error) {
	return e.svc.GetSessionMeta(ref)
}

// GetMessages returns one page of a session transcript.
func (e *Engine) GetMessages(ctx context.Context, ref model.SessionRef, offset int, limit int) (MessagesPage, error) {
	return e.svc.GetMessages(ctx, ref, offset, limit)
}

// GetBlob returns provider-attached content referenced by a message part.
func (e *Engine) GetBlob(ctx context.Context, ref model.SessionRef, key string) (BlobResponse, error) {
	return e.svc.GetBlob(ctx, ref, key)
}

// Search queries the full-text search index.
func (e *Engine) Search(ctx context.Context, query string, filter index.SearchFilter) ([]index.SearchHit, error) {
	e.mu.Lock()
	refresher := e.refresher
	e.mu.Unlock()
	if refresher == nil || refresher.DB() == nil {
		return []index.SearchHit{}, nil
	}
	return refresher.DB().Search(ctx, query, filter)
}

// IndexProgress returns the status of background FTS indexing.
func (e *Engine) IndexProgress(ctx context.Context) (index.FTSProgress, error) {
	e.mu.Lock()
	refresher := e.refresher
	e.mu.Unlock()
	if refresher == nil || refresher.DB() == nil {
		return index.FTSProgress{}, nil
	}
	return refresher.DB().Progress(ctx)
}

// CopyResumeCommand builds the provider resume command for a session.
func (e *Engine) CopyResumeCommand(_ context.Context, ref model.SessionRef) (string, error) {
	return e.svc.CopyResumeCommand(ref)
}

// RevealSource reveals the session's source directory path.
func (e *Engine) RevealSource(_ context.Context, ref model.SessionRef) (string, error) {
	return e.svc.RevealSource(ref)
}

// Diagnostics returns aggregated scanner health.
func (e *Engine) Diagnostics(_ context.Context) (provider.Diagnostics, error) {
	return e.svc.Diagnostics()
}

func (e *Engine) ensureManage(ctx context.Context) (*manage.Manager, error) {
	e.manageMu.Lock()
	defer e.manageMu.Unlock()

	if e.manageOverride != nil {
		return e.manageOverride, nil
	}
	if e.manage != nil {
		return e.manage, nil
	}

	roots := e.roots
	if roots.Config == "" {
		r, err := paths.Default()
		if err != nil {
			return nil, fmt.Errorf("manage: resolve roots: %w", err)
		}
		roots = r
	}
	cfgPath := filepath.Join(roots.Config, "config.toml")

	opts := []manage.Option{}
	if e.svc != nil {
		if p, ok := e.svc.Providers().Get(model.AgentClaude); ok {
			if ld, ok := p.(provider.LiveDetector); ok {
				opts = append(opts, manage.WithLiveFunc(func(_ context.Context, agent, id string) (bool, error) {
					if agent != string(model.AgentClaude) {
						return false, nil
					}
					parent := id
					if i := strings.Index(id, "/agent-"); i > 0 {
						parent = id[:i]
					}
					live, err := ld.Live(ctx)
					if err != nil {
						return false, err
					}
					_, ok := live[parent]
					return ok, nil
				}))
			}
		}
	}

	mgr, err := manage.New(roots, cfgPath, opts...)
	if err != nil {
		return nil, fmt.Errorf("manage: init: %w", err)
	}
	e.manage = mgr
	return mgr, nil
}

// PreviewDelete plans a destructive action over the given sessions.
func (e *Engine) PreviewDelete(ctx context.Context, refs []model.SessionRef) (DeletePreview, error) {
	if len(refs) == 0 {
		return DeletePreview{}, errors.New("manage: no sessions specified")
	}
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return DeletePreview{}, err
	}
	if e.svc == nil || e.svc.Catalog() == nil {
		return DeletePreview{}, errors.New("manage: catalog not initialized")
	}
	if err := e.svc.ValidateRefs(refs); err != nil {
		return DeletePreview{}, err
	}
	p, err := mgr.Preview(ctx, refs, e.svc.Catalog().All())
	if err != nil {
		return DeletePreview{}, err
	}
	return DeletePreview{Items: p.Items, TotalBytes: p.TotalBytes, Token: p.Token}, nil
}

// DeleteSessions executes a confirmed destructive plan.
func (e *Engine) DeleteSessions(ctx context.Context, refs []model.SessionRef, token string) (DeleteReport, error) {
	if len(refs) == 0 {
		return DeleteReport{}, errors.New("manage: no sessions specified")
	}
	if strings.TrimSpace(token) == "" {
		return DeleteReport{}, ErrPreviewStale
	}
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return DeleteReport{}, err
	}
	if e.svc == nil || e.svc.Catalog() == nil {
		return DeleteReport{}, errors.New("manage: catalog not initialized")
	}
	if err := e.svc.ValidateRefs(refs); err != nil {
		return DeleteReport{}, err
	}
	rep, err := mgr.Delete(ctx, refs, e.svc.Catalog().All(), token)
	if err != nil {
		return DeleteReport{}, err
	}

	out := DeleteReport{
		Items:      rep.Items,
		Deleted:    rep.Deleted,
		Failed:     rep.Failed,
		FreedBytes: rep.FreedBytes,
		Forgotten:  rep.Forgotten,
	}
	if len(rep.Forgotten) > 0 {
		e.forget(ctx, rep.Forgotten)
	}
	return out, nil
}

// SetManageOverride overrides the destructive action manager (tests only).
func (e *Engine) SetManageOverride(mgr *manage.Manager) {
	e.manageMu.Lock()
	defer e.manageMu.Unlock()
	e.manageOverride = mgr
}

// TrashSupported reports whether the host system supports moving files to trash.
func (e *Engine) TrashSupported() bool {
	return manage.PlatformTrashSupported()
}

// Forget drops deleted sessions from the cache DB, catalog, and transcript
// LRU, then notifies subscribers.
func (e *Engine) Forget(ctx context.Context, refs []model.SessionRef) {
	e.forget(ctx, refs)
}

func (e *Engine) forget(ctx context.Context, refs []model.SessionRef) {
	e.mu.Lock()
	refresher := e.refresher
	events := e.events
	e.mu.Unlock()

	if refresher == nil || refresher.Forget(ctx, refs) != nil {
		if e.svc != nil && e.svc.Catalog() != nil {
			e.svc.Catalog().Remove(refs)
		}
	}
	if e.svc != nil {
		for _, ref := range refs {
			e.svc.EvictTranscript(ref.Key())
		}
	}
	if events != nil {
		events.NoteChanged(nil, refs)
	}
}

// GetSettings surfaces the current manage configuration.
func (e *Engine) GetSettings(ctx context.Context) (Settings, error) {
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return Settings{}, err
	}
	cfg, err := mgr.Config()
	if err != nil {
		return Settings{}, err
	}
	return Settings{Enabled: cfg.Enabled, AllowPermanentDelete: cfg.AllowPermanentDelete}, nil
}

// SetManageEnabled toggles destructive actions.
func (e *Engine) SetManageEnabled(ctx context.Context, enabled bool) (Settings, error) {
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return Settings{}, err
	}
	cfg, err := mgr.Config()
	if err != nil {
		return Settings{}, err
	}
	cfg.Enabled = enabled
	if err := mgr.SetConfig(cfg); err != nil {
		return Settings{}, err
	}
	return Settings{Enabled: cfg.Enabled, AllowPermanentDelete: cfg.AllowPermanentDelete}, nil
}

// SetAllowPermanentDelete toggles the permanent-delete allowance.
func (e *Engine) SetAllowPermanentDelete(ctx context.Context, allow bool) (Settings, error) {
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return Settings{}, err
	}
	cfg, err := mgr.Config()
	if err != nil {
		return Settings{}, err
	}
	cfg.AllowPermanentDelete = allow
	if err := mgr.SetConfig(cfg); err != nil {
		return Settings{}, err
	}
	return Settings{Enabled: cfg.Enabled, AllowPermanentDelete: cfg.AllowPermanentDelete}, nil
}

// BuildHandoff renders the handoff document preview.
func (e *Engine) BuildHandoff(ctx context.Context, req HandoffRequest) (HandoffPreview, error) {
	return e.svc.BuildHandoff(ctx, req)
}

// HandoffCommand writes the handoff files and returns the launch command.
func (e *Engine) HandoffCommand(ctx context.Context, req HandoffRequest) (string, error) {
	return e.svc.HandoffCommand(ctx, req)
}

// SaveHandoff writes the self-contained full handoff document to destPath.
func (e *Engine) SaveHandoff(ctx context.Context, req HandoffRequest, destPath string) (string, error) {
	return e.svc.SaveHandoff(ctx, req, destPath)
}

// RenderHandoff builds the self-contained full handoff document text.
func (e *Engine) RenderHandoff(ctx context.Context, req HandoffRequest) (string, error) {
	return e.svc.RenderHandoff(ctx, req)
}

// HandoffCache reports the handoff files kept on disk.
func (e *Engine) HandoffCache(_ context.Context) (HandoffCacheInfo, error) {
	return e.svc.HandoffCache(), nil
}

// ClearHandoffCache deletes the handoff files kept on disk.
func (e *Engine) ClearHandoffCache(_ context.Context) (HandoffCacheInfo, error) {
	return e.svc.ClearHandoffCache()
}

// PreviewExport estimates an export without writing anything.
func (e *Engine) PreviewExport(ctx context.Context, req ExportRequest) (ExportPreview, error) {
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return ExportPreview{}, err
	}
	return e.svc.PreviewExport(ctx, req, mgr)
}

// ExportBundle writes the bundle to destPath.
func (e *Engine) ExportBundle(ctx context.Context, req ExportRequest, destPath string) (string, error) {
	mgr, err := e.ensureManage(ctx)
	if err != nil {
		return "", err
	}
	return e.svc.ExportBundle(ctx, req, destPath, mgr)
}

// OpenBundle opens and validates a bundle from disk.
func (e *Engine) OpenBundle(ctx context.Context, path string) (BundleSummary, error) {
	return e.svc.OpenBundle(ctx, path)
}

// OpenBundleBytes opens and validates a bundle from raw bytes.
func (e *Engine) OpenBundleBytes(ctx context.Context, name string, data []byte) (BundleSummary, error) {
	return e.svc.OpenBundleBytes(ctx, name, data)
}

// BuildBundleHandoff generates a handoff preview from an opened bundle.
func (e *Engine) BuildBundleHandoff(ctx context.Context, req BundleHandoffRequest) (HandoffPreview, error) {
	return e.svc.BuildBundleHandoff(ctx, req)
}

// BundleHandoffCommand writes the handoff files and returns the launch command.
func (e *Engine) BundleHandoffCommand(ctx context.Context, req BundleHandoffRequest) (string, error) {
	return e.svc.BundleHandoffCommand(ctx, req)
}

// SaveBundleHandoff writes the full handoff document to destPath.
func (e *Engine) SaveBundleHandoff(ctx context.Context, req BundleHandoffRequest, destPath string) (string, error) {
	return e.svc.SaveBundleHandoff(ctx, req, destPath)
}

// RenderBundleHandoff builds the self-contained full handoff document text for an opened bundle.
func (e *Engine) RenderBundleHandoff(ctx context.Context, req BundleHandoffRequest) (string, error) {
	return e.svc.RenderBundleHandoff(ctx, req)
}
