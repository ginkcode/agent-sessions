package app

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// Destructive-action sentinels aliased from internal/engine.
var (
	ErrManageDisabled      = engine.ErrManageDisabled
	ErrSessionLive         = engine.ErrSessionLive
	ErrPathOutsideRoot     = engine.ErrPathOutsideRoot
	ErrUnsupportedAction   = engine.ErrUnsupportedAction
	ErrPreviewStale        = engine.ErrPreviewStale
	ErrPermanentNotAllowed = engine.ErrPermanentNotAllowed
)

// Type aliases to internal/engine.
type (
	DeletePreview = engine.DeletePreview
	DeleteReport  = engine.DeleteReport
	Settings      = engine.Settings
)

// PreviewDelete plans a destructive action over the given sessions.
func (a *App) PreviewDelete(refs []model.SessionRef) (DeletePreview, error) {
	return a.activeBackend().PreviewDelete(a.appCtx(), refs)
}

// DeleteSessions executes a confirmed destructive plan.
func (a *App) DeleteSessions(refs []model.SessionRef, token string) (DeleteReport, error) {
	return a.activeBackend().DeleteSessions(a.appCtx(), refs, token)
}

// GetSettings surfaces the current manage configuration.
func (a *App) GetSettings() (Settings, error) {
	return a.activeBackend().GetSettings(a.appCtx())
}

// SetManageEnabled turns destructive actions on or off.
func (a *App) SetManageEnabled(enabled bool) (Settings, error) {
	return a.activeBackend().SetManageEnabled(a.appCtx(), enabled)
}

// SetAllowPermanentDelete toggles the permanent-delete allowance.
func (a *App) SetAllowPermanentDelete(allow bool) (Settings, error) {
	return a.activeBackend().SetAllowPermanentDelete(a.appCtx(), allow)
}

func (a *App) manageCtx() context.Context {
	return a.appCtx()
}

// forget drops deleted sessions from the cache DB, catalog, and transcript
// LRU, then notifies subscribers.
func (a *App) forget(refs []model.SessionRef) {
	if a.localEngine != nil {
		a.localEngine.Forget(a.appCtx(), refs)
	}
}
