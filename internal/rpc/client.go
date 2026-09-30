package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Ensure Client implements engine.Backend.
var _ engine.Backend = (*Client)(nil)

// NotificationHandler receives incoming JSON-RPC notifications.
type NotificationHandler func(method string, params json.RawMessage)

// Client implements engine.Backend by forwarding requests over a JSON-RPC 2.0 transport.
type Client struct {
	rawR     io.Reader
	rawW     io.Writer
	reader   *FrameReader
	writer   *FrameWriter
	seq      int64
	mu       sync.Mutex
	pending  map[string]chan *Response
	handlers []NotificationHandler
	emitter  engine.Emitter
	closed   bool
	done     chan struct{}
	closeErr error

	caps   Capabilities
	roots  paths.Roots
	appVer string
}

// NewClient creates a new RPC client over r and w and starts reading frames in a background goroutine.
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		rawR:    r,
		rawW:    w,
		reader:  NewFrameReader(r),
		writer:  NewFrameWriter(w),
		pending: make(map[string]chan *Response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Initialize performs the handshake with the remote server.
func (c *Client) Initialize(ctx context.Context, req InitializeRequest) (*InitializeResult, error) {
	if req.ProtocolVersion == 0 {
		req.ProtocolVersion = ProtocolVersion
	}
	var res InitializeResult
	if err := c.call(ctx, MethodInitialize, req, &res); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.caps = res.Capabilities
	c.roots = res.Roots
	c.appVer = res.AppVersion
	c.mu.Unlock()
	return &res, nil
}

// Capabilities returns the remote server capabilities negotiated during initialize.
func (c *Client) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Roots returns the remote server storage roots discovered during initialize.
func (c *Client) Roots() paths.Roots {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.roots
}

// RemoteAppVersion returns the remote server application version.
func (c *Client) RemoteAppVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appVer
}

// OnNotification registers a callback invoked when a notification arrives.
func (c *Client) OnNotification(h NotificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, h)
}

// SetEmitter registers an emitter to receive catalog:changed and index:progress notifications.
func (c *Client) SetEmitter(emitter engine.Emitter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emitter = emitter
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	c.mu.Lock()
	handlers := append([]NotificationHandler(nil), c.handlers...)
	emitter := c.emitter
	c.mu.Unlock()

	for _, h := range handlers {
		h(method, params)
	}

	if emitter != nil {
		switch method {
		case NotificationCatalogChanged:
			var ev engine.CatalogChanged
			if err := json.Unmarshal(params, &ev); err == nil {
				emitter.Emit(NotificationCatalogChanged, ev)
			}
		case NotificationIndexProgress:
			var p index.FTSProgress
			if err := json.Unmarshal(params, &p); err == nil {
				emitter.Emit(NotificationIndexProgress, p)
			}
		}
	}
}

func (c *Client) readLoop() {
	var closeErr error
	defer func() {
		c.mu.Lock()
		c.closed = true
		c.closeErr = closeErr
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = make(map[string]chan *Response)
		c.mu.Unlock()
		close(c.done)
	}()

	for {
		frame, err := c.reader.ReadFrame()
		if err != nil {
			closeErr = err
			return
		}

		var partial struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *ResponseError  `json:"error"`
		}
		if err := json.Unmarshal(frame, &partial); err != nil {
			continue
		}

		if partial.Method != "" && partial.ID == nil {
			c.handleNotification(partial.Method, partial.Params)
			continue
		}

		if partial.ID != nil {
			// Deliver once: taking the entry out under the lock means a
			// duplicate or late response for this ID finds nothing, so this
			// send never blocks the read loop (the channel holds one).
			key := fmt.Sprintf("%v", partial.ID)
			c.mu.Lock()
			ch, ok := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if ok && ch != nil {
				ch <- &Response{
					JSONRPC: JSONRPCVersion,
					ID:      partial.ID,
					Result:  partial.Result,
					Error:   partial.Error,
				}
			}
		}
	}
}

func (c *Client) sendNotification(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = data
	}
	return c.writer.WriteFrame(Notification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  raw,
	})
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrDisconnected
	}
	id := atomic.AddInt64(&c.seq, 1)
	key := fmt.Sprintf("%d", id)
	ch := make(chan *Response, 1)
	c.pending[key] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	var rawParams json.RawMessage
	if params != nil {
		pData, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = pData
	}

	req := Request{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	if err := c.writer.WriteFrame(req); err != nil {
		if errors.Is(err, ErrPayloadTooLarge) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrDisconnected, err)
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return ErrDisconnected
		}
		if resp.Error != nil {
			return FromRPCError(resp.Error)
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("failed to unmarshal rpc result: %w", err)
			}
		}
		return nil

	case <-ctx.Done():
		_ = c.sendNotification(MethodCancelRequest, CancelParams{ID: id})
		return ctx.Err()

	case <-c.done:
		return ErrDisconnected
	}
}

// Close closes the client connection.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	if closer, ok := c.rawR.(io.Closer); ok {
		_ = closer.Close()
	}
	if closer, ok := c.rawW.(io.Closer); ok {
		_ = closer.Close()
	}
	return nil
}

// Heartbeat tells the server the client is still there. It needs no reply,
// so it also works before initialize finishes.
func (c *Client) Heartbeat() error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrDisconnected
	}
	return c.sendNotification(MethodHeartbeat, nil)
}

// Done returns a channel that is closed when the client connection terminates.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// --- engine.Backend implementation ---

func (c *Client) Ping(ctx context.Context, name string) (string, error) {
	var out string
	err := c.call(ctx, "ping", PingParams{Name: name}, &out)
	return out, err
}

func (c *Client) Scan(ctx context.Context) error {
	return c.call(ctx, "scan", nil, nil)
}

func (c *Client) ListGroups(ctx context.Context, mode engine.GroupMode, filter engine.FilterOpts) ([]engine.GroupNode, error) {
	var out []engine.GroupNode
	err := c.call(ctx, "listGroups", ListGroupsParams{Mode: mode, Filter: filter}, &out)
	return out, err
}

func (c *Client) ListSessions(ctx context.Context, groupKey string, filter engine.FilterOpts, sort engine.SortOpts) ([]model.SessionMeta, error) {
	var out []model.SessionMeta
	err := c.call(ctx, "listSessions", ListSessionsParams{GroupKey: groupKey, Filter: filter, Sort: sort}, &out)
	return out, err
}

func (c *Client) AgentCounts(ctx context.Context, filter engine.FilterOpts) (map[string]int, error) {
	var out map[string]int
	err := c.call(ctx, "agentCounts", AgentCountsParams{Filter: filter}, &out)
	return out, err
}

func (c *Client) GetSessionMeta(ctx context.Context, ref model.SessionRef) (model.SessionMeta, error) {
	var out model.SessionMeta
	err := c.call(ctx, "getSessionMeta", GetSessionMetaParams{Ref: ref}, &out)
	return out, err
}

func (c *Client) GetMessages(ctx context.Context, ref model.SessionRef, offset int, limit int) (engine.MessagesPage, error) {
	var out engine.MessagesPage
	err := c.call(ctx, "getMessages", GetMessagesParams{Ref: ref, Offset: offset, Limit: limit}, &out)
	return out, err
}

func (c *Client) GetBlob(ctx context.Context, ref model.SessionRef, key string) (engine.BlobResponse, error) {
	var out engine.BlobResponse
	err := c.call(ctx, "getBlob", GetBlobParams{Ref: ref, Key: key}, &out)
	return out, err
}

func (c *Client) Search(ctx context.Context, query string, filter index.SearchFilter) ([]index.SearchHit, error) {
	var out []index.SearchHit
	err := c.call(ctx, "search", SearchParams{Query: query, Filter: filter}, &out)
	return out, err
}

func (c *Client) IndexProgress(ctx context.Context) (index.FTSProgress, error) {
	var out index.FTSProgress
	err := c.call(ctx, "indexProgress", nil, &out)
	return out, err
}

func (c *Client) CopyResumeCommand(ctx context.Context, ref model.SessionRef) (string, error) {
	var out string
	err := c.call(ctx, "copyResumeCommand", CopyResumeCommandParams{Ref: ref}, &out)
	return out, err
}

func (c *Client) RevealSource(ctx context.Context, ref model.SessionRef) (string, error) {
	var out string
	err := c.call(ctx, "revealSource", RevealSourceParams{Ref: ref}, &out)
	return out, err
}

func (c *Client) Diagnostics(ctx context.Context) (provider.Diagnostics, error) {
	var out provider.Diagnostics
	err := c.call(ctx, "diagnostics", nil, &out)
	return out, err
}

func (c *Client) GetSettings(ctx context.Context) (engine.Settings, error) {
	var out engine.Settings
	err := c.call(ctx, "getSettings", nil, &out)
	return out, err
}

func (c *Client) SetManageEnabled(ctx context.Context, enabled bool) (engine.Settings, error) {
	var out engine.Settings
	err := c.call(ctx, "setManageEnabled", SetManageEnabledParams{Enabled: enabled}, &out)
	return out, err
}

func (c *Client) SetAllowPermanentDelete(ctx context.Context, allow bool) (engine.Settings, error) {
	var out engine.Settings
	err := c.call(ctx, "setAllowPermanentDelete", SetAllowPermanentDeleteParams{Allow: allow}, &out)
	return out, err
}

func (c *Client) PreviewDelete(ctx context.Context, refs []model.SessionRef) (engine.DeletePreview, error) {
	var out engine.DeletePreview
	err := c.call(ctx, "previewDelete", PreviewDeleteParams{Refs: refs}, &out)
	return out, err
}

func (c *Client) DeleteSessions(ctx context.Context, refs []model.SessionRef, token string) (engine.DeleteReport, error) {
	var out engine.DeleteReport
	err := c.call(ctx, "deleteSessions", DeleteSessionsParams{Refs: refs, Token: token}, &out)
	return out, err
}

func (c *Client) BuildHandoff(ctx context.Context, req engine.HandoffRequest) (engine.HandoffPreview, error) {
	var out engine.HandoffPreview
	err := c.call(ctx, "buildHandoff", BuildHandoffParams{Req: req}, &out)
	return out, err
}

func (c *Client) HandoffCommand(ctx context.Context, req engine.HandoffRequest) (string, error) {
	var out string
	err := c.call(ctx, "handoffCommand", HandoffCommandParams{Req: req}, &out)
	return out, err
}

func (c *Client) SaveHandoff(ctx context.Context, req engine.HandoffRequest, destPath string) (string, error) {
	var out string
	err := c.call(ctx, "saveHandoff", SaveHandoffParams{Req: req, DestPath: destPath}, &out)
	return out, err
}

func (c *Client) RenderHandoff(ctx context.Context, req engine.HandoffRequest) (string, error) {
	var out string
	err := c.call(ctx, "renderHandoff", RenderHandoffParams{Req: req}, &out)
	return out, err
}

func (c *Client) HandoffCache(ctx context.Context) (engine.HandoffCacheInfo, error) {
	var out engine.HandoffCacheInfo
	err := c.call(ctx, "handoffCache", nil, &out)
	return out, err
}

func (c *Client) ClearHandoffCache(ctx context.Context) (engine.HandoffCacheInfo, error) {
	var out engine.HandoffCacheInfo
	err := c.call(ctx, "clearHandoffCache", nil, &out)
	return out, err
}

func (c *Client) PreviewExport(ctx context.Context, req engine.ExportRequest) (engine.ExportPreview, error) {
	var out engine.ExportPreview
	err := c.call(ctx, "previewExport", PreviewExportParams{Req: req}, &out)
	return out, err
}

func (c *Client) ExportBundle(ctx context.Context, req engine.ExportRequest, destPath string) (string, error) {
	var out string
	err := c.call(ctx, "exportBundle", ExportBundleParams{Req: req, DestPath: destPath}, &out)
	return out, err
}

func (c *Client) OpenBundle(ctx context.Context, path string) (engine.BundleSummary, error) {
	var out engine.BundleSummary
	err := c.call(ctx, "openBundle", OpenBundleParams{Path: path}, &out)
	return out, err
}

func (c *Client) OpenBundleBytes(ctx context.Context, name string, data []byte) (engine.BundleSummary, error) {
	var out engine.BundleSummary
	err := c.call(ctx, "openBundleBytes", OpenBundleBytesParams{Name: name, Data: data}, &out)
	return out, err
}

func (c *Client) BuildBundleHandoff(ctx context.Context, req engine.BundleHandoffRequest) (engine.HandoffPreview, error) {
	var out engine.HandoffPreview
	err := c.call(ctx, "buildBundleHandoff", BuildBundleHandoffParams{Req: req}, &out)
	return out, err
}

func (c *Client) BundleHandoffCommand(ctx context.Context, req engine.BundleHandoffRequest) (string, error) {
	var out string
	err := c.call(ctx, "bundleHandoffCommand", BundleHandoffCommandParams{Req: req}, &out)
	return out, err
}

func (c *Client) SaveBundleHandoff(ctx context.Context, req engine.BundleHandoffRequest, destPath string) (string, error) {
	var out string
	err := c.call(ctx, "saveBundleHandoff", SaveBundleHandoffParams{Req: req, DestPath: destPath}, &out)
	return out, err
}

func (c *Client) RenderBundleHandoff(ctx context.Context, req engine.BundleHandoffRequest) (string, error) {
	var out string
	err := c.call(ctx, "renderBundleHandoff", RenderBundleHandoffParams{Req: req}, &out)
	return out, err
}
