package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// Starter builds the backend during initialize, after the client's env
// overrides are applied, so the roots, the cache lock and the engine all see
// them. ctx lives as long as Serve.
type Starter func(ctx context.Context) (engine.Backend, Capabilities, paths.Roots, error)

// Server handles JSON-RPC 2.0 requests over stdio and forwards to an engine.Backend.
type Server struct {
	backend   engine.Backend
	starter   Starter
	reader    *FrameReader
	writer    *FrameWriter
	cancelReg *CancelRegistry
	// initState: 0 not started, 1 running, 2 done. A failed initialize
	// keeps initErr and is not retried.
	initState   int
	initialized bool
	initErr     error
	mu          sync.Mutex
	handlers    sync.WaitGroup
	closeOnce   sync.Once
	closed      chan struct{}
	idleTimeout time.Duration
}

// NewServer wraps a backend with JSON-RPC stdio transport.
func NewServer(backend engine.Backend, r io.Reader, w io.Writer) *Server {
	return &Server{
		backend:   backend,
		reader:    NewFrameReader(r),
		writer:    NewFrameWriter(w),
		cancelReg: NewCancelRegistry(),
		closed:    make(chan struct{}),
	}
}

// SetStarter makes initialize build the backend with fn instead of using the
// one passed to NewServer.
func (s *Server) SetStarter(fn Starter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starter = fn
}

// SetIdleTimeout makes Serve return ErrIdleTimeout when no frame arrives for
// d. The client heartbeats well inside d, so this fires only when it is gone
// but the transport never closed, such as a half-open ssh connection whose
// sshd has not noticed yet. Zero disables it.
func (s *Server) SetIdleTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleTimeout = d
}

// Notify sends a JSON-RPC notification to the client.
func (s *Server) Notify(method string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.writer.WriteFrame(Notification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  data,
	})
}

// Serve reads and processes frames until EOF or context cancellation. On
// return it has cancelled and waited for every in-flight handler.
func (s *Server) Serve(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer func() {
		cancel()
		s.handlers.Wait()
	}()

	// Reads run apart from the loop so the idle timer and ctx can end Serve
	// while a read is blocked on a client that went silent.
	frames := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		for {
			frame, err := s.reader.ReadFrame()
			if err != nil {
				readErr <- err
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	s.mu.Lock()
	idleTimeout := s.idleTimeout
	s.mu.Unlock()
	var idle <-chan time.Time
	var idleTimer *time.Timer
	if idleTimeout > 0 {
		idleTimer = time.NewTimer(idleTimeout)
		defer idleTimer.Stop()
		idle = idleTimer.C
	}

	for {
		var frame []byte
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.closed:
			return nil
		case <-idle:
			return ErrIdleTimeout
		case err := <-readErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case frame = <-frames:
		}
		if idleTimer != nil {
			idleTimer.Reset(idleTimeout)
		}

		// Try to parse as Request
		var req Request
		if err := json.Unmarshal(frame, &req); err != nil {
			_ = s.writer.WriteFrame(Response{
				JSONRPC: JSONRPCVersion,
				Error: &ResponseError{
					Code:    CodeParseError,
					Message: fmt.Sprintf("invalid json: %v", err),
				},
			})
			continue
		}

		// Handle notifications (no ID). A heartbeat needs nothing beyond
		// the idle reset above.
		if req.ID == nil {
			if req.Method == MethodCancelRequest {
				var cancelParams CancelParams
				if err := json.Unmarshal(req.Params, &cancelParams); err == nil {
					s.cancelReg.Cancel(cancelParams.ID)
				}
			}
			continue
		}

		// Handshake enforcement: must initialize first
		if !s.isInitialized() && req.Method != MethodInitialize {
			_ = s.sendError(req.ID, &ResponseError{
				Code:    CodeInvalidRequest,
				Message: "server not initialized; call initialize first",
			})
			continue
		}

		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			s.handleRequest(ctx, req)
		}()
	}
}

func (s *Server) isInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

func (s *Server) sendResult(id any, result any) error {
	var raw json.RawMessage
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return s.sendError(id, ToRPCError(err))
		}
		raw = data
	}
	return s.writer.WriteFrame(Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  raw,
	})
}

func (s *Server) sendError(id any, rpcErr *ResponseError) error {
	return s.writer.WriteFrame(Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   rpcErr,
	})
}

// allowlistedEnvKeys defines environment variables that the client may override.
var allowlistedEnvKeys = map[string]bool{
	"CLAUDE_CONFIG_DIR": true,
	"CODEX_HOME":        true,
	"XDG_CONFIG_HOME":   true,
	"XDG_DATA_HOME":     true,
	"XDG_CACHE_HOME":    true,
	"XDG_STATE_HOME":    true,
	"XDG_RUNTIME_DIR":   true,
}

func (s *Server) handleInitialize(serveCtx context.Context, req Request) (any, error) {
	s.mu.Lock()
	switch {
	case s.initErr != nil:
		err := s.initErr
		s.mu.Unlock()
		return nil, ToRPCError(err)
	case s.initState != 0:
		s.mu.Unlock()
		return nil, &ResponseError{Code: CodeInvalidRequest, Message: "initialize already called"}
	}
	s.initState = 1
	starter := s.starter
	s.mu.Unlock()

	res, err := s.initialize(serveCtx, req, starter)

	s.mu.Lock()
	s.initState = 2
	if err != nil {
		s.initErr = err
	} else {
		s.initialized = true
	}
	s.mu.Unlock()
	if err != nil {
		return nil, ToRPCError(err)
	}
	return res, nil
}

func (s *Server) initialize(serveCtx context.Context, req Request, starter Starter) (InitializeResult, error) {
	var initReq InitializeRequest
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &initReq); err != nil {
			return InitializeResult{}, &ResponseError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("invalid initialize params: %v", err),
			}
		}
	}

	if initReq.ProtocolVersion != 0 && initReq.ProtocolVersion != ProtocolVersion {
		return InitializeResult{}, &ResponseError{
			Code:    CodeInvalidRequest,
			Message: fmt.Sprintf("unsupported protocol version %d (server requires %d)", initReq.ProtocolVersion, ProtocolVersion),
			Data:    &ErrorData{Code: ErrCodeProtocolMismatch},
		}
	}

	// Apply allowlisted client env variables to the process environment
	// before anything resolves roots; agent CLIs the engine runs inherit them.
	for k, v := range initReq.ClientEnv {
		if allowlistedEnvKeys[strings.ToUpper(k)] {
			_ = os.Setenv(k, v)
		}
	}

	caps := Capabilities{
		Trash:  true,
		Manage: true,
		Export: true,
		Import: true,
		Search: true,
	}
	var roots paths.Roots
	if starter != nil {
		backend, c, r, err := starter(serveCtx)
		if err != nil {
			return InitializeResult{}, err
		}
		caps, roots = c, r
		s.mu.Lock()
		s.backend = backend
		s.mu.Unlock()
	} else {
		roots, _ = paths.Default()
	}

	return InitializeResult{
		ProtocolVersion: ProtocolVersion,
		AppVersion:      version.Current(),
		Capabilities:    caps,
		Roots:           roots,
	}, nil
}

func (s *Server) handleRequest(parentCtx context.Context, req Request) {
	callCtx, cancel := context.WithCancel(parentCtx)
	s.cancelReg.Register(req.ID, cancel)
	defer func() {
		s.cancelReg.Unregister(req.ID)
		cancel()
	}()

	var res any
	var err error
	if req.Method == MethodInitialize {
		res, err = s.handleInitialize(parentCtx, req)
	} else {
		res, err = s.dispatch(callCtx, req)
	}
	if err != nil {
		_ = s.sendError(req.ID, ToRPCError(err))
		return
	}
	_ = s.sendResult(req.ID, res)
}

func (s *Server) dispatch(ctx context.Context, req Request) (any, error) {
	s.mu.Lock()
	backend := s.backend
	s.mu.Unlock()
	if backend == nil {
		return nil, &ResponseError{Code: CodeInvalidRequest, Message: "server not initialized; call initialize first"}
	}
	switch req.Method {
	case "ping":
		var p PingParams
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		return backend.Ping(ctx, p.Name)

	case "scan":
		return nil, backend.Scan(ctx)

	case "listGroups":
		var p ListGroupsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.ListGroups(ctx, p.Mode, p.Filter)

	case "listSessions":
		var p ListSessionsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.ListSessions(ctx, p.GroupKey, p.Filter, p.Sort)

	case "agentCounts":
		var p AgentCountsParams
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		return backend.AgentCounts(ctx, p.Filter)

	case "getSessionMeta":
		var p GetSessionMetaParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.GetSessionMeta(ctx, p.Ref)

	case "getMessages":
		var p GetMessagesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.GetMessages(ctx, p.Ref, p.Offset, p.Limit)

	case "getBlob":
		var p GetBlobParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.GetBlob(ctx, p.Ref, p.Key)

	case "search":
		var p SearchParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.Search(ctx, p.Query, p.Filter)

	case "indexProgress":
		return backend.IndexProgress(ctx)

	case "copyResumeCommand":
		var p CopyResumeCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.CopyResumeCommand(ctx, p.Ref)

	case "revealSource":
		var p RevealSourceParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.RevealSource(ctx, p.Ref)

	case "diagnostics":
		return backend.Diagnostics(ctx)

	case "getSettings":
		return backend.GetSettings(ctx)

	case "setManageEnabled":
		var p SetManageEnabledParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.SetManageEnabled(ctx, p.Enabled)

	case "setAllowPermanentDelete":
		var p SetAllowPermanentDeleteParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.SetAllowPermanentDelete(ctx, p.Allow)

	case "previewDelete":
		var p PreviewDeleteParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.PreviewDelete(ctx, p.Refs)

	case "deleteSessions":
		var p DeleteSessionsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.DeleteSessions(ctx, p.Refs, p.Token)

	case "buildHandoff":
		var p BuildHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.BuildHandoff(ctx, p.Req)

	case "handoffCommand":
		var p HandoffCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.HandoffCommand(ctx, p.Req)

	case "saveHandoff":
		var p SaveHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.SaveHandoff(ctx, p.Req, p.DestPath)

	case "renderHandoff":
		var p RenderHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.RenderHandoff(ctx, p.Req)

	case "handoffCache":
		return backend.HandoffCache(ctx)

	case "clearHandoffCache":
		return backend.ClearHandoffCache(ctx)

	case "previewExport":
		var p PreviewExportParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.PreviewExport(ctx, p.Req)

	case "exportBundle":
		var p ExportBundleParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.ExportBundle(ctx, p.Req, p.DestPath)

	case "openBundle":
		var p OpenBundleParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.OpenBundle(ctx, p.Path)

	case "openBundleBytes":
		var p OpenBundleBytesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.OpenBundleBytes(ctx, p.Name, p.Data)

	case "buildBundleHandoff":
		var p BuildBundleHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.BuildBundleHandoff(ctx, p.Req)

	case "bundleHandoffCommand":
		var p BundleHandoffCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.BundleHandoffCommand(ctx, p.Req)

	case "saveBundleHandoff":
		var p SaveBundleHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.SaveBundleHandoff(ctx, p.Req, p.DestPath)

	case "renderBundleHandoff":
		var p RenderBundleHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return backend.RenderBundleHandoff(ctx, p.Req)

	default:
		return nil, &ResponseError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		}
	}
}

// Close closes the server.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return nil
}
