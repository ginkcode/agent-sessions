package engine

import (
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
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
	TrashLabel string        `json:"trashLabel,omitempty"`
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
