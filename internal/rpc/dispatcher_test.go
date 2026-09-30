package rpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// mockBackend implements engine.Backend for RPC tests.
type mockBackend struct {
	pingCount   int64
	scanCount   int64
	slowBlockCh chan struct{}
	slowCalled  chan struct{}
	errToReturn error
}

var _ engine.Backend = (*mockBackend)(nil)

func (m *mockBackend) Ping(ctx context.Context, name string) (string, error) {
	atomic.AddInt64(&m.pingCount, 1)
	if m.slowBlockCh != nil {
		if m.slowCalled != nil {
			close(m.slowCalled)
		}
		select {
		case <-m.slowBlockCh:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if m.errToReturn != nil {
		return "", m.errToReturn
	}
	return "pong " + name, nil
}

func (m *mockBackend) Scan(ctx context.Context) error {
	atomic.AddInt64(&m.scanCount, 1)
	return m.errToReturn
}

func (m *mockBackend) ListGroups(ctx context.Context, mode engine.GroupMode, filter engine.FilterOpts) ([]engine.GroupNode, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return []engine.GroupNode{
		{Key: "group-1", Label: "Group 1"},
	}, nil
}

func (m *mockBackend) ListSessions(ctx context.Context, groupKey string, filter engine.FilterOpts, sort engine.SortOpts) ([]model.SessionMeta, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return []model.SessionMeta{
		{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "s-1"}, Title: "Session 1"},
	}, nil
}

func (m *mockBackend) AgentCounts(ctx context.Context, filter engine.FilterOpts) (map[string]int, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return map[string]int{"claude-code": 10}, nil
}

func (m *mockBackend) GetSessionMeta(ctx context.Context, ref model.SessionRef) (model.SessionMeta, error) {
	if m.errToReturn != nil {
		return model.SessionMeta{}, m.errToReturn
	}
	return model.SessionMeta{Ref: ref, Title: "Meta for " + ref.ID}, nil
}

func (m *mockBackend) GetMessages(ctx context.Context, ref model.SessionRef, offset int, limit int) (engine.MessagesPage, error) {
	if m.errToReturn != nil {
		return engine.MessagesPage{}, m.errToReturn
	}
	return engine.MessagesPage{
		Messages: []model.Message{
			{
				ID:    "m-1",
				Parts: []model.Part{{Kind: model.PartText, Text: "hello"}},
			},
		},
		TotalCount: 1,
	}, nil
}

func (m *mockBackend) GetBlob(ctx context.Context, ref model.SessionRef, key string) (engine.BlobResponse, error) {
	if m.errToReturn != nil {
		return engine.BlobResponse{}, m.errToReturn
	}
	return engine.BlobResponse{Data: "blob-data", Mime: "text/plain", IsBinary: false}, nil
}

func (m *mockBackend) Search(ctx context.Context, query string, filter index.SearchFilter) ([]index.SearchHit, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return []index.SearchHit{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "s-1"}, Score: 1.0}}, nil
}

func (m *mockBackend) IndexProgress(ctx context.Context) (index.FTSProgress, error) {
	return index.FTSProgress{Done: 42, Pending: 100}, nil
}

func (m *mockBackend) CopyResumeCommand(ctx context.Context, ref model.SessionRef) (string, error) {
	return "claude --resume " + ref.ID, nil
}

func (m *mockBackend) RevealSource(ctx context.Context, ref model.SessionRef) (string, error) {
	return "/path/to/" + ref.ID, nil
}

func (m *mockBackend) Diagnostics(ctx context.Context) (provider.Diagnostics, error) {
	return provider.Diagnostics{}, nil
}

func (m *mockBackend) GetSettings(ctx context.Context) (engine.Settings, error) {
	return engine.Settings{Enabled: true}, nil
}

func (m *mockBackend) SetManageEnabled(ctx context.Context, enabled bool) (engine.Settings, error) {
	return engine.Settings{Enabled: enabled}, nil
}

func (m *mockBackend) SetAllowPermanentDelete(ctx context.Context, allow bool) (engine.Settings, error) {
	return engine.Settings{AllowPermanentDelete: allow}, nil
}

func (m *mockBackend) PreviewDelete(ctx context.Context, refs []model.SessionRef) (engine.DeletePreview, error) {
	if m.errToReturn != nil {
		return engine.DeletePreview{}, m.errToReturn
	}
	return engine.DeletePreview{Token: "test-token"}, nil
}

func (m *mockBackend) DeleteSessions(ctx context.Context, refs []model.SessionRef, token string) (engine.DeleteReport, error) {
	if m.errToReturn != nil {
		return engine.DeleteReport{}, m.errToReturn
	}
	return engine.DeleteReport{Deleted: len(refs)}, nil
}

func (m *mockBackend) BuildHandoff(ctx context.Context, req engine.HandoffRequest) (engine.HandoffPreview, error) {
	return engine.HandoffPreview{PromptMarkdown: "# Handoff"}, nil
}

func (m *mockBackend) HandoffCommand(ctx context.Context, req engine.HandoffRequest) (string, error) {
	return "cat handoff.md", nil
}

func (m *mockBackend) SaveHandoff(ctx context.Context, req engine.HandoffRequest, destPath string) (string, error) {
	return destPath, nil
}

func (m *mockBackend) RenderHandoff(ctx context.Context, req engine.HandoffRequest) (string, error) {
	return "rendered markdown", nil
}

func (m *mockBackend) HandoffCache(ctx context.Context) (engine.HandoffCacheInfo, error) {
	return engine.HandoffCacheInfo{Files: 3}, nil
}

func (m *mockBackend) ClearHandoffCache(ctx context.Context) (engine.HandoffCacheInfo, error) {
	return engine.HandoffCacheInfo{Files: 0}, nil
}

func (m *mockBackend) PreviewExport(ctx context.Context, req engine.ExportRequest) (engine.ExportPreview, error) {
	return engine.ExportPreview{Sessions: 2}, nil
}

func (m *mockBackend) ExportBundle(ctx context.Context, req engine.ExportRequest, destPath string) (string, error) {
	return destPath, nil
}

func (m *mockBackend) OpenBundle(ctx context.Context, path string) (engine.BundleSummary, error) {
	return engine.BundleSummary{Path: path, SessionsCount: 1}, nil
}

func (m *mockBackend) OpenBundleBytes(ctx context.Context, name string, data []byte) (engine.BundleSummary, error) {
	return engine.BundleSummary{Path: name, SessionsCount: 1}, nil
}

func (m *mockBackend) BuildBundleHandoff(ctx context.Context, req engine.BundleHandoffRequest) (engine.HandoffPreview, error) {
	return engine.HandoffPreview{PromptMarkdown: "bundle handoff"}, nil
}

func (m *mockBackend) BundleHandoffCommand(ctx context.Context, req engine.BundleHandoffRequest) (string, error) {
	return "bundle handoff cmd", nil
}

func (m *mockBackend) SaveBundleHandoff(ctx context.Context, req engine.BundleHandoffRequest, destPath string) (string, error) {
	return destPath, nil
}

func (m *mockBackend) RenderBundleHandoff(ctx context.Context, req engine.BundleHandoffRequest) (string, error) {
	return "rendered bundle handoff", nil
}

func setupPipePair(mock *mockBackend) (*Server, *Client, func()) {
	// clientWriter -> serverReader
	c2sReader, c2sWriter := io.Pipe()
	// serverWriter -> clientReader
	s2cReader, s2cWriter := io.Pipe()

	server := NewServer(mock, c2sReader, s2cWriter)
	client := NewClient(s2cReader, c2sWriter)

	ctx, cancel := context.WithCancel(context.Background())
	var serverWg sync.WaitGroup
	serverWg.Add(1)
	go func() {
		defer serverWg.Done()
		_ = server.Serve(ctx)
	}()

	cleanup := func() {
		cancel()
		_ = client.Close()
		_ = server.Close()
		_ = c2sReader.Close()
		_ = c2sWriter.Close()
		_ = s2cReader.Close()
		_ = s2cWriter.Close()
		serverWg.Wait()
	}

	return server, client, cleanup
}

func TestDispatcher_HandshakeEnforcement(t *testing.T) {
	mock := &mockBackend{}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()

	// Calling ping before initialize should fail
	_, err := client.Ping(context.Background(), "world")
	if err == nil {
		t.Fatal("expected error calling ping before initialize, got nil")
	}

	// Now initialize
	initRes, err := client.Initialize(context.Background(), InitializeRequest{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      "v0.3.0",
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if initRes.ProtocolVersion != ProtocolVersion {
		t.Errorf("got protocol version %d, want %d", initRes.ProtocolVersion, ProtocolVersion)
	}
	if !initRes.Capabilities.Manage {
		t.Errorf("expected manage capability to be true")
	}

	// Calling ping after initialize should succeed
	pong, err := client.Ping(context.Background(), "world")
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if pong != "pong world" {
		t.Errorf("got %q, want %q", pong, "pong world")
	}
}

func TestDispatcher_RoundTripMethods(t *testing.T) {
	mock := &mockBackend{}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Scan
	if err := client.Scan(ctx); err != nil {
		t.Errorf("Scan failed: %v", err)
	}
	if atomic.LoadInt64(&mock.scanCount) != 1 {
		t.Errorf("scan count = %d, want 1", mock.scanCount)
	}

	// ListGroups
	groups, err := client.ListGroups(ctx, engine.GroupModeDirAgent, engine.FilterOpts{})
	if err != nil {
		t.Fatalf("ListGroups failed: %v", err)
	}
	if len(groups) != 1 || groups[0].Key != "group-1" {
		t.Errorf("unexpected groups: %+v", groups)
	}

	// ListSessions
	sessions, err := client.ListSessions(ctx, "group-1", engine.FilterOpts{}, engine.SortOpts{})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Ref.ID != "s-1" {
		t.Errorf("unexpected sessions: %+v", sessions)
	}

	// AgentCounts
	counts, err := client.AgentCounts(ctx, engine.FilterOpts{})
	if err != nil {
		t.Fatalf("AgentCounts failed: %v", err)
	}
	if counts["claude-code"] != 10 {
		t.Errorf("unexpected agent counts: %+v", counts)
	}

	// GetSessionMeta
	meta, err := client.GetSessionMeta(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "s-1"})
	if err != nil {
		t.Fatalf("GetSessionMeta failed: %v", err)
	}
	if meta.Title != "Meta for s-1" {
		t.Errorf("unexpected meta: %+v", meta)
	}

	// GetMessages
	page, err := client.GetMessages(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "s-1"}, 0, 10)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(page.Messages) != 1 || len(page.Messages[0].Parts) != 1 || page.Messages[0].Parts[0].Text != "hello" {
		t.Errorf("unexpected page: %+v", page)
	}

	// Search
	hits, err := client.Search(ctx, "query", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(hits) != 1 || hits[0].Ref.ID != "s-1" {
		t.Errorf("unexpected hits: %+v", hits)
	}

	// CopyResumeCommand
	cmd, err := client.CopyResumeCommand(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "s-1"})
	if err != nil || cmd != "claude --resume s-1" {
		t.Errorf("unexpected CopyResumeCommand: %q, err: %v", cmd, err)
	}

	// PreviewDelete & DeleteSessions
	preview, err := client.PreviewDelete(ctx, []model.SessionRef{{Agent: model.AgentClaude, ID: "s-1"}})
	if err != nil || preview.Token != "test-token" {
		t.Errorf("unexpected PreviewDelete: %+v, err: %v", preview, err)
	}

	delReport, err := client.DeleteSessions(ctx, []model.SessionRef{{Agent: model.AgentClaude, ID: "s-1"}}, "test-token")
	if err != nil || delReport.Deleted != 1 {
		t.Errorf("unexpected DeleteSessions: %+v, err: %v", delReport, err)
	}
}

func TestDispatcher_SentinelErrors(t *testing.T) {
	mock := &mockBackend{errToReturn: engine.ErrUnknownSession}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	_, err := client.GetSessionMeta(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "non-existent"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, engine.ErrUnknownSession) {
		t.Errorf("expected errors.Is(err, engine.ErrUnknownSession), got %v", err)
	}

	// Test manage disabled sentinel
	mock.errToReturn = engine.ErrManageDisabled
	_, err = client.DeleteSessions(ctx, []model.SessionRef{{Agent: model.AgentClaude, ID: "s-1"}}, "token")
	if !errors.Is(err, engine.ErrManageDisabled) {
		t.Errorf("expected errors.Is(err, engine.ErrManageDisabled), got %v", err)
	}
}

type testEmitter struct {
	mu     sync.Mutex
	events map[string]any
}

func (e *testEmitter) Emit(name string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events[name] = payload
}

func TestDispatcher_Notifications(t *testing.T) {
	mock := &mockBackend{}
	server, client, cleanup := setupPipePair(mock)
	defer cleanup()

	ctx := context.Background()
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	emitter := &testEmitter{events: make(map[string]any)}
	client.SetEmitter(emitter)

	// Send catalog:changed notification from server
	ev := engine.CatalogChanged{Changed: []model.SessionRef{{Agent: model.AgentClaude, ID: "s-new"}}}
	if err := server.Notify(NotificationCatalogChanged, ev); err != nil {
		t.Fatalf("failed to notify catalog changed: %v", err)
	}

	// Wait briefly for notification propagation
	var received bool
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		emitter.mu.Lock()
		_, ok := emitter.events[NotificationCatalogChanged]
		emitter.mu.Unlock()
		if ok {
			received = true
			break
		}
	}

	if !received {
		t.Errorf("client emitter did not receive %s notification", NotificationCatalogChanged)
	}
}

func TestDispatcher_Cancellation(t *testing.T) {
	mock := &mockBackend{
		slowBlockCh: make(chan struct{}),
		slowCalled:  make(chan struct{}),
	}
	_, client, cleanup := setupPipePair(mock)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := client.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := client.Ping(ctx, "slow")
		errCh <- err
	}()

	// Wait until backend.Ping is entered
	select {
	case <-mock.slowCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ping to be called")
	}

	// Cancel client context
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for client call to return after cancel")
	}
}
