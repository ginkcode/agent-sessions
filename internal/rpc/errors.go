package rpc

import (
	"errors"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeAppError       = -32000
)

// Stable application error codes carried in ErrorData.Code.
const (
	ErrCodeUnknownSession   = "unknown_session"
	ErrCodeUnknownProvider  = "unknown_provider"
	ErrCodeUnknownGroup     = "unknown_group"
	ErrCodeManageDisabled   = "manage_disabled"
	ErrCodePreviewStale     = "preview_stale"
	ErrCodeSessionLive      = "session_live"
	ErrCodePathOutsideRoot  = "path_outside_root"
	ErrCodeUnsupported      = "unsupported"
	ErrCodeRemoteBusy       = "remote_busy"
	ErrCodeDisconnected     = "disconnected"
	ErrCodeCancelled        = "cancelled"
	ErrCodeProtocolMismatch = "protocol_mismatch"
	ErrCodePayloadTooLarge  = "payload_too_large"
	ErrCodeTimeout          = "timeout"
)

// Client-side and server-side sentinel errors.
var (
	// ErrRemoteBusy is no longer returned: clients share a host (see
	// internal/engine/cache.go). The code stays so it keeps decoding.
	ErrRemoteBusy       = errors.New("remote server is busy with another session")
	ErrDisconnected     = errors.New("disconnected from remote host")
	ErrCancelled        = errors.New("request cancelled")
	ErrProtocolMismatch = errors.New("rpc protocol version mismatch")
	ErrPayloadTooLarge  = errors.New("payload exceeds maximum frame size")
	ErrPrefaceNotFound  = errors.New("preface nonce not found before scan cap")
	ErrTimeout          = errors.New("request timed out")
	ErrIdleTimeout      = errors.New("no message from the client within the idle timeout")
)

// ErrorData attaches machine-readable diagnostic codes to a JSON-RPC error.
type ErrorData struct {
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

// ResponseError represents a JSON-RPC 2.0 error object.
type ResponseError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *ErrorData `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	if e == nil {
		return ""
	}
	if e.Data != nil && e.Data.Code != "" {
		return fmt.Sprintf("rpc error %d (%s): %s", e.Code, e.Data.Code, e.Message)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// ToRPCError maps a Go error to a standard JSON-RPC 2.0 ResponseError.
func ToRPCError(err error) *ResponseError {
	if err == nil {
		return nil
	}
	var rpcErr *ResponseError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}

	code := CodeAppError
	var strCode string

	switch {
	case errors.Is(err, engine.ErrUnknownSession):
		strCode = ErrCodeUnknownSession
	case errors.Is(err, engine.ErrUnknownProvider):
		strCode = ErrCodeUnknownProvider
	case errors.Is(err, engine.ErrUnknownGroup):
		strCode = ErrCodeUnknownGroup
	case errors.Is(err, engine.ErrManageDisabled):
		strCode = ErrCodeManageDisabled
	case errors.Is(err, engine.ErrPreviewStale):
		strCode = ErrCodePreviewStale
	case errors.Is(err, engine.ErrSessionLive):
		strCode = ErrCodeSessionLive
	case errors.Is(err, engine.ErrPathOutsideRoot):
		strCode = ErrCodePathOutsideRoot
	case errors.Is(err, engine.ErrUnsupportedAction):
		strCode = ErrCodeUnsupported
	case errors.Is(err, ErrRemoteBusy):
		strCode = ErrCodeRemoteBusy
	case errors.Is(err, ErrDisconnected):
		strCode = ErrCodeDisconnected
	case errors.Is(err, ErrCancelled):
		strCode = ErrCodeCancelled
	case errors.Is(err, ErrProtocolMismatch):
		strCode = ErrCodeProtocolMismatch
	case errors.Is(err, ErrPayloadTooLarge):
		strCode = ErrCodePayloadTooLarge
	case errors.Is(err, ErrTimeout):
		strCode = ErrCodeTimeout
	default:
		code = CodeInternalError
	}

	var data *ErrorData
	if strCode != "" {
		data = &ErrorData{Code: strCode}
	}

	return &ResponseError{
		Code:    code,
		Message: err.Error(),
		Data:    data,
	}
}

// FromRPCError maps a JSON-RPC ResponseError back to a Go error, matching sentinels.
func FromRPCError(respErr *ResponseError) error {
	if respErr == nil {
		return nil
	}
	if respErr.Data == nil || respErr.Data.Code == "" {
		return respErr
	}

	var sentinel error
	switch respErr.Data.Code {
	case ErrCodeUnknownSession:
		sentinel = engine.ErrUnknownSession
	case ErrCodeUnknownProvider:
		sentinel = engine.ErrUnknownProvider
	case ErrCodeUnknownGroup:
		sentinel = engine.ErrUnknownGroup
	case ErrCodeManageDisabled:
		sentinel = engine.ErrManageDisabled
	case ErrCodePreviewStale:
		sentinel = engine.ErrPreviewStale
	case ErrCodeSessionLive:
		sentinel = engine.ErrSessionLive
	case ErrCodePathOutsideRoot:
		sentinel = engine.ErrPathOutsideRoot
	case ErrCodeUnsupported:
		sentinel = engine.ErrUnsupportedAction
	case ErrCodeRemoteBusy:
		sentinel = ErrRemoteBusy
	case ErrCodeDisconnected:
		sentinel = ErrDisconnected
	case ErrCodeCancelled:
		sentinel = ErrCancelled
	case ErrCodeProtocolMismatch:
		sentinel = ErrProtocolMismatch
	case ErrCodePayloadTooLarge:
		sentinel = ErrPayloadTooLarge
	case ErrCodeTimeout:
		sentinel = ErrTimeout
	}

	if sentinel != nil {
		return fmt.Errorf("%w: %s", sentinel, respErr.Message)
	}
	return respErr
}
