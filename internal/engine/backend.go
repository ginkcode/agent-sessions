package engine

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Backend is the unified, context-first interface implemented by both the
// local Engine and the remote RPC client.
type Backend interface {
	Ping(ctx context.Context, name string) (string, error)
	Scan(ctx context.Context) error

	// Catalog & Navigation
	ListGroups(ctx context.Context, mode GroupMode, filter FilterOpts) ([]GroupNode, error)
	ListSessions(ctx context.Context, groupKey string, filter FilterOpts, sort SortOpts) ([]model.SessionMeta, error)
	AgentCounts(ctx context.Context, filter FilterOpts) (map[string]int, error)
	GetSessionMeta(ctx context.Context, ref model.SessionRef) (model.SessionMeta, error)
	GetMessages(ctx context.Context, ref model.SessionRef, offset int, limit int) (MessagesPage, error)
	GetBlob(ctx context.Context, ref model.SessionRef, key string) (BlobResponse, error)

	// Search
	Search(ctx context.Context, query string, filter index.SearchFilter) ([]index.SearchHit, error)
	IndexProgress(ctx context.Context) (index.FTSProgress, error)

	// Actions & Diagnostics
	CopyResumeCommand(ctx context.Context, ref model.SessionRef) (string, error)
	RevealSource(ctx context.Context, ref model.SessionRef) (string, error)
	Diagnostics(ctx context.Context) (provider.Diagnostics, error)

	// Manage
	GetSettings(ctx context.Context) (Settings, error)
	SetManageEnabled(ctx context.Context, enabled bool) (Settings, error)
	SetAllowPermanentDelete(ctx context.Context, allow bool) (Settings, error)
	PreviewDelete(ctx context.Context, refs []model.SessionRef) (DeletePreview, error)
	DeleteSessions(ctx context.Context, refs []model.SessionRef, token string) (DeleteReport, error)

	// Handoff
	BuildHandoff(ctx context.Context, req HandoffRequest) (HandoffPreview, error)
	HandoffCommand(ctx context.Context, req HandoffRequest) (string, error)
	SaveHandoff(ctx context.Context, req HandoffRequest, destPath string) (string, error)
	RenderHandoff(ctx context.Context, req HandoffRequest) (string, error)
	HandoffCache(ctx context.Context) (HandoffCacheInfo, error)
	ClearHandoffCache(ctx context.Context) (HandoffCacheInfo, error)

	// Export & Import
	PreviewExport(ctx context.Context, req ExportRequest) (ExportPreview, error)
	ExportBundle(ctx context.Context, req ExportRequest, destPath string) (string, error)
	OpenBundle(ctx context.Context, path string) (BundleSummary, error)
	OpenBundleBytes(ctx context.Context, name string, data []byte) (BundleSummary, error)
	BuildBundleHandoff(ctx context.Context, req BundleHandoffRequest) (HandoffPreview, error)
	BundleHandoffCommand(ctx context.Context, req BundleHandoffRequest) (string, error)
	SaveBundleHandoff(ctx context.Context, req BundleHandoffRequest, destPath string) (string, error)
	RenderBundleHandoff(ctx context.Context, req BundleHandoffRequest) (string, error)
}
