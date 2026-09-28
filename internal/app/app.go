// Package app implements the application service and desktop bindings.
package app

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
	"github.com/ginkcode/agent-sessions/internal/scan"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the desktop application service exposed to the Wails frontend.
type App struct {
	ctx    context.Context
	svc    *Service
	runner *scan.Runner
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
		svc:    svc,
		runner: runner,
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
		svc:    svc,
		runner: runner,
	}
}

// OnStartup is invoked by Wails when the runtime is ready.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx
	if a.runner != nil {
		go func() {
			_ = a.Scan()
		}()
	}
}

// Scan triggers a scan across all providers and updates the service state.
func (a *App) Scan() error {
	if a.runner == nil || a.svc == nil {
		return nil
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	report := a.runner.Run(ctx, a.svc.states)
	a.svc.ApplyReport(report)
	if a.ctx != nil && a.ctx.Value("events") != nil {
		wruntime.EventsEmit(a.ctx, "scan:ready")
	}
	return nil
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

