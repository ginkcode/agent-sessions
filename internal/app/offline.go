package app

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// offlineBackend stands in for a selected remote host that is not connected.
// Every call fails with rpc.ErrDisconnected, so nothing (least of all a
// delete) silently runs against Local instead.
type offlineBackend struct{ host string }

var _ engine.Backend = offlineBackend{}

func (b offlineBackend) err() error { return fmt.Errorf("%w %s", rpc.ErrDisconnected, b.host) }

func (b offlineBackend) Ping(context.Context, string) (string, error) { return "", b.err() }
func (b offlineBackend) Scan(context.Context) error                   { return b.err() }

func (b offlineBackend) ListGroups(context.Context, engine.GroupMode, engine.FilterOpts) ([]engine.GroupNode, error) {
	return nil, b.err()
}

func (b offlineBackend) ListSessions(context.Context, string, engine.FilterOpts, engine.SortOpts) ([]model.SessionMeta, error) {
	return nil, b.err()
}

func (b offlineBackend) AgentCounts(context.Context, engine.FilterOpts) (map[string]int, error) {
	return nil, b.err()
}

func (b offlineBackend) GetSessionMeta(context.Context, model.SessionRef) (model.SessionMeta, error) {
	return model.SessionMeta{}, b.err()
}

func (b offlineBackend) GetMessages(context.Context, model.SessionRef, int, int) (engine.MessagesPage, error) {
	return engine.MessagesPage{}, b.err()
}

func (b offlineBackend) GetBlob(context.Context, model.SessionRef, string) (engine.BlobResponse, error) {
	return engine.BlobResponse{}, b.err()
}

func (b offlineBackend) Search(context.Context, string, index.SearchFilter) ([]index.SearchHit, error) {
	return nil, b.err()
}

func (b offlineBackend) IndexProgress(context.Context) (index.FTSProgress, error) {
	return index.FTSProgress{}, b.err()
}

func (b offlineBackend) CopyResumeCommand(context.Context, model.SessionRef) (string, error) {
	return "", b.err()
}

func (b offlineBackend) RevealSource(context.Context, model.SessionRef) (string, error) {
	return "", b.err()
}

func (b offlineBackend) Diagnostics(context.Context) (provider.Diagnostics, error) {
	return provider.Diagnostics{}, b.err()
}

func (b offlineBackend) GetSettings(context.Context) (engine.Settings, error) {
	return engine.Settings{}, b.err()
}

func (b offlineBackend) SetManageEnabled(context.Context, bool) (engine.Settings, error) {
	return engine.Settings{}, b.err()
}

func (b offlineBackend) SetAllowPermanentDelete(context.Context, bool) (engine.Settings, error) {
	return engine.Settings{}, b.err()
}

func (b offlineBackend) PreviewDelete(context.Context, []model.SessionRef) (engine.DeletePreview, error) {
	return engine.DeletePreview{}, b.err()
}

func (b offlineBackend) DeleteSessions(context.Context, []model.SessionRef, string) (engine.DeleteReport, error) {
	return engine.DeleteReport{}, b.err()
}

func (b offlineBackend) BuildHandoff(context.Context, engine.HandoffRequest) (engine.HandoffPreview, error) {
	return engine.HandoffPreview{}, b.err()
}

func (b offlineBackend) HandoffCommand(context.Context, engine.HandoffRequest) (string, error) {
	return "", b.err()
}

func (b offlineBackend) SaveHandoff(context.Context, engine.HandoffRequest, string) (string, error) {
	return "", b.err()
}

func (b offlineBackend) RenderHandoff(context.Context, engine.HandoffRequest) (string, error) {
	return "", b.err()
}

func (b offlineBackend) HandoffCache(context.Context) (engine.HandoffCacheInfo, error) {
	return engine.HandoffCacheInfo{}, b.err()
}

func (b offlineBackend) ClearHandoffCache(context.Context) (engine.HandoffCacheInfo, error) {
	return engine.HandoffCacheInfo{}, b.err()
}

func (b offlineBackend) PreviewExport(context.Context, engine.ExportRequest) (engine.ExportPreview, error) {
	return engine.ExportPreview{}, b.err()
}

func (b offlineBackend) ExportBundle(context.Context, engine.ExportRequest, string) (string, error) {
	return "", b.err()
}

func (b offlineBackend) OpenBundle(context.Context, string) (engine.BundleSummary, error) {
	return engine.BundleSummary{}, b.err()
}

func (b offlineBackend) OpenBundleBytes(context.Context, string, []byte) (engine.BundleSummary, error) {
	return engine.BundleSummary{}, b.err()
}

func (b offlineBackend) BuildBundleHandoff(context.Context, engine.BundleHandoffRequest) (engine.HandoffPreview, error) {
	return engine.HandoffPreview{}, b.err()
}

func (b offlineBackend) BundleHandoffCommand(context.Context, engine.BundleHandoffRequest) (string, error) {
	return "", b.err()
}

func (b offlineBackend) SaveBundleHandoff(context.Context, engine.BundleHandoffRequest, string) (string, error) {
	return "", b.err()
}

func (b offlineBackend) RenderBundleHandoff(context.Context, engine.BundleHandoffRequest) (string, error) {
	return "", b.err()
}
