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

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// Server handles JSON-RPC 2.0 requests over stdio and forwards to an engine.Backend.
type Server struct {
	backend     engine.Backend
	reader      *FrameReader
	writer      *FrameWriter
	cancelReg   *CancelRegistry
	initialized bool
	initErr     error
	caps        Capabilities
	capsSet     bool
	mu          sync.Mutex
	closeOnce   sync.Once
	closed      chan struct{}
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

// SetCapabilities overrides the capabilities returned during initialize.
func (s *Server) SetCapabilities(caps Capabilities) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.caps = caps
	s.capsSet = true
}

// SetInitError forces handleInitialize to reject initialize with the given error.
func (s *Server) SetInitError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initErr = err
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

// Serve reads and processes frames until EOF or context cancellation.
func (s *Server) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.closed:
			return nil
		default:
		}

		frame, err := s.reader.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
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

		// Handle notifications (no ID)
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

		go s.handleRequest(ctx, req)
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

func (s *Server) handleInitialize(req Request) (any, error) {
	s.mu.Lock()
	if s.initErr != nil {
		err := s.initErr
		s.mu.Unlock()
		return nil, ToRPCError(err)
	}
	s.mu.Unlock()

	var initReq InitializeRequest
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &initReq); err != nil {
			return nil, &ResponseError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("invalid initialize params: %v", err),
			}
		}
	}

	if initReq.ProtocolVersion != 0 && initReq.ProtocolVersion != ProtocolVersion {
		return nil, &ResponseError{
			Code:    CodeInvalidRequest,
			Message: fmt.Sprintf("unsupported protocol version %d (server requires %d)", initReq.ProtocolVersion, ProtocolVersion),
			Data:    &ErrorData{Code: ErrCodeProtocolMismatch},
		}
	}

	// Apply allowlisted client env variables to the process environment
	for k, v := range initReq.ClientEnv {
		if allowlistedEnvKeys[strings.ToUpper(k)] {
			_ = os.Setenv(k, v)
		}
	}

	roots, _ := paths.Default()

	caps := Capabilities{
		Trash:  true,
		Manage: true,
		Export: true,
		Import: true,
		Search: true,
	}

	s.mu.Lock()
	s.initialized = true
	if s.capsSet {
		caps = s.caps
	}
	s.mu.Unlock()

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

	res, err := s.dispatch(callCtx, req)
	if err != nil {
		_ = s.sendError(req.ID, ToRPCError(err))
		return
	}
	_ = s.sendResult(req.ID, res)
}

func (s *Server) dispatch(ctx context.Context, req Request) (any, error) {
	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req)

	case "ping":
		var p PingParams
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		return s.backend.Ping(ctx, p.Name)

	case "scan":
		return nil, s.backend.Scan(ctx)

	case "listGroups":
		var p ListGroupsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.ListGroups(ctx, p.Mode, p.Filter)

	case "listSessions":
		var p ListSessionsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.ListSessions(ctx, p.GroupKey, p.Filter, p.Sort)

	case "agentCounts":
		var p AgentCountsParams
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		return s.backend.AgentCounts(ctx, p.Filter)

	case "getSessionMeta":
		var p GetSessionMetaParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.GetSessionMeta(ctx, p.Ref)

	case "getMessages":
		var p GetMessagesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.GetMessages(ctx, p.Ref, p.Offset, p.Limit)

	case "getBlob":
		var p GetBlobParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.GetBlob(ctx, p.Ref, p.Key)

	case "search":
		var p SearchParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.Search(ctx, p.Query, p.Filter)

	case "indexProgress":
		return s.backend.IndexProgress(ctx)

	case "copyResumeCommand":
		var p CopyResumeCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.CopyResumeCommand(ctx, p.Ref)

	case "revealSource":
		var p RevealSourceParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.RevealSource(ctx, p.Ref)

	case "diagnostics":
		return s.backend.Diagnostics(ctx)

	case "getSettings":
		return s.backend.GetSettings(ctx)

	case "setManageEnabled":
		var p SetManageEnabledParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.SetManageEnabled(ctx, p.Enabled)

	case "setAllowPermanentDelete":
		var p SetAllowPermanentDeleteParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.SetAllowPermanentDelete(ctx, p.Allow)

	case "previewDelete":
		var p PreviewDeleteParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.PreviewDelete(ctx, p.Refs)

	case "deleteSessions":
		var p DeleteSessionsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.DeleteSessions(ctx, p.Refs, p.Token)

	case "buildHandoff":
		var p BuildHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.BuildHandoff(ctx, p.Req)

	case "handoffCommand":
		var p HandoffCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.HandoffCommand(ctx, p.Req)

	case "saveHandoff":
		var p SaveHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.SaveHandoff(ctx, p.Req, p.DestPath)

	case "renderHandoff":
		var p RenderHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.RenderHandoff(ctx, p.Req)

	case "handoffCache":
		return s.backend.HandoffCache(ctx)

	case "clearHandoffCache":
		return s.backend.ClearHandoffCache(ctx)

	case "previewExport":
		var p PreviewExportParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.PreviewExport(ctx, p.Req)

	case "exportBundle":
		var p ExportBundleParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.ExportBundle(ctx, p.Req, p.DestPath)

	case "openBundle":
		var p OpenBundleParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.OpenBundle(ctx, p.Path)

	case "openBundleBytes":
		var p OpenBundleBytesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.OpenBundleBytes(ctx, p.Name, p.Data)

	case "buildBundleHandoff":
		var p BuildBundleHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.BuildBundleHandoff(ctx, p.Req)

	case "bundleHandoffCommand":
		var p BundleHandoffCommandParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.BundleHandoffCommand(ctx, p.Req)

	case "saveBundleHandoff":
		var p SaveBundleHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.SaveBundleHandoff(ctx, p.Req, p.DestPath)

	case "renderBundleHandoff":
		var p RenderBundleHandoffParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return s.backend.RenderBundleHandoff(ctx, p.Req)

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
