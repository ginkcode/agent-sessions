package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Destructive-action sentinels re-exposed for the frontend and tests; each
// aliases its manage package origin so errors.Is keeps working across the
// binding boundary.
var (
	ErrManageDisabled      = manage.ErrDisabled
	ErrSessionLive         = manage.ErrLive
	ErrPathOutsideRoot     = manage.ErrPathOutsideRoot
	ErrUnsupportedAction   = manage.ErrUnsupportedAction
	ErrPreviewStale        = manage.ErrPreviewStale
	ErrPermanentNotAllowed = manage.ErrPermanentNotAllowed
)

// DeletePreview mirrors manage.Preview for the frontend.
type DeletePreview struct {
	Items      []manage.Item `json:"items"`
	TotalBytes int64         `json:"totalBytes"`
	Token      string        `json:"token"`
}

// DeleteReport mirrors manage.Report for the frontend.
type DeleteReport struct {
	Items      []manage.Result    `json:"items"`
	Deleted    int                `json:"deleted"`
	Failed     int                `json:"failed"`
	FreedBytes int64              `json:"freedBytes"`
	Forgotten  []model.SessionRef `json:"forgotten"`
}

// Settings is the user's persisted destructive-action configuration.
type Settings struct {
	Enabled              bool `json:"enabled"`
	AllowPermanentDelete bool `json:"allowPermanentDelete"`
}

// ensureManage lazily constructs the app's manage.Manager from resolved
// provider roots. It is safe to call concurrently from bindings; a failed
// construction is retried on the next call.
func (a *App) ensureManage() error {
	a.manageMu.Lock()
	defer a.manageMu.Unlock()
	if a.manageOverride != nil {
		a.manage = a.manageOverride
		return nil
	}
	if a.manage != nil {
		return nil
	}
	roots, err := paths.Default()
	if err != nil {
		return fmt.Errorf("manage: resolve roots: %w", err)
	}
	cfgPath := filepath.Join(roots.Config, "config.toml")

	// Claude liveness rides the provider's existing detector, keyed by the
	// parent session ID: a live child means its parent is live too.
	opts := []manage.Option{}
	if p, ok := a.svc.providers.Get(model.AgentClaude); ok {
		if ld, ok := p.(provider.LiveDetector); ok {
			opts = append(opts, manage.WithLiveFunc(func(_ context.Context, agent, id string) (bool, error) {
				if agent != string(model.AgentClaude) {
					return false, nil
				}
				parent := id
				if i := strings.Index(id, "/agent-"); i > 0 {
					parent = id[:i]
				}
				live, err := ld.Live(a.manageCtx())
				if err != nil {
					return false, err
				}
				_, ok := live[parent]
				return ok, nil
			}))
		}
	}

	mgr, err := manage.New(roots, cfgPath, opts...)
	if err != nil {
		return fmt.Errorf("manage: init: %w", err)
	}
	a.manage = mgr
	return nil
}

// PreviewDelete plans a destructive action over the given sessions.
func (a *App) PreviewDelete(refs []model.SessionRef) (DeletePreview, error) {
	if len(refs) == 0 {
		return DeletePreview{}, errors.New("manage: no sessions specified")
	}
	if err := a.ensureManage(); err != nil {
		return DeletePreview{}, err
	}
	if err := a.svc.validateRefs(refs); err != nil {
		return DeletePreview{}, err
	}
	p, err := a.manage.Preview(a.manageCtx(), refs, a.svc.catalog.All())
	if err != nil {
		return DeletePreview{}, err
	}
	return DeletePreview{Items: p.Items, TotalBytes: p.TotalBytes, Token: p.Token}, nil
}

// DeleteSessions executes a confirmed destructive plan. On any per-item
// success it forgets the deleted refs from the cache and catalog, evicts
// their transcripts, and notifies the UI.
func (a *App) DeleteSessions(refs []model.SessionRef, token string) (DeleteReport, error) {
	if len(refs) == 0 {
		return DeleteReport{}, errors.New("manage: no sessions specified")
	}
	if strings.TrimSpace(token) == "" {
		return DeleteReport{}, ErrPreviewStale
	}
	if err := a.ensureManage(); err != nil {
		return DeleteReport{}, err
	}
	if err := a.svc.validateRefs(refs); err != nil {
		return DeleteReport{}, err
	}
	rep, err := a.manage.Delete(a.manageCtx(), refs, a.svc.catalog.All(), token)
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
		a.forget(rep.Forgotten)
	}
	return out, nil
}

// GetSettings surfaces the current manage configuration.
func (a *App) GetSettings() (Settings, error) {
	if err := a.ensureManage(); err != nil {
		return Settings{}, err
	}
	cfg, err := a.manage.Config()
	if err != nil {
		return Settings{}, err
	}
	return Settings{Enabled: cfg.Enabled, AllowPermanentDelete: cfg.AllowPermanentDelete}, nil
}

// SetManageEnabled turns destructive actions on or off.
func (a *App) SetManageEnabled(enabled bool) (Settings, error) {
	return a.setSettings(func(cfg *manage.Config) { cfg.Enabled = enabled })
}

// SetAllowPermanentDelete toggles the permanent-delete allowance.
func (a *App) SetAllowPermanentDelete(allow bool) (Settings, error) {
	return a.setSettings(func(cfg *manage.Config) { cfg.AllowPermanentDelete = allow })
}

func (a *App) setSettings(mutate func(*manage.Config)) (Settings, error) {
	if err := a.ensureManage(); err != nil {
		return Settings{}, err
	}
	cfg, err := a.manage.Config()
	if err != nil {
		return Settings{}, err
	}
	mutate(&cfg)
	if err := a.manage.SetConfig(cfg); err != nil {
		return Settings{}, err
	}
	return Settings{Enabled: cfg.Enabled, AllowPermanentDelete: cfg.AllowPermanentDelete}, nil
}

// manageCtx returns the runtime context or a background fallback.
func (a *App) manageCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// validateRefs checks that refs still exist in the current catalog.
func (s *Service) validateRefs(refs []model.SessionRef) error {
	for _, ref := range refs {
		if _, ok := s.catalog.Get(ref); !ok {
			return fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
		}
	}
	return nil
}

// forget drops deleted sessions from the cache DB, catalog, and transcript
// LRU, then notifies the UI. It takes every provider's single-flight lock
// via refresher.Forget so an in-flight Refresh cannot resurrect a row.
func (a *App) forget(refs []model.SessionRef) {
	// Cache cleanup is best-effort: the sources are gone, so the next full
	// rebuild drops stale rows anyway. The catalog must still drop them now
	// so the UI never offers a deleted session.
	if a.refresher == nil || a.refresher.Forget(a.manageCtx(), refs) != nil {
		if a.svc != nil && a.svc.catalog != nil {
			a.svc.catalog.Remove(refs)
		}
	}

	if a.svc != nil {
		for _, ref := range refs {
			a.svc.evictTranscript(ref.Key())
		}
	}

	if a.ctx != nil && a.ctx.Value("events") != nil {
		wruntime.EventsEmit(a.ctx, "scan:ready")
	}
}

// evictTranscript drops one session's cached transcript.
func (s *Service) evictTranscript(key string) {
	s.transcripts.evict(key)
}
