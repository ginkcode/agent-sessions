package rpc

import (
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// Current protocol version.
const ProtocolVersion = 1

// JSONRPCVersion is the protocol version tag for JSON-RPC 2.0.
const JSONRPCVersion = "2.0"

// Standard notification and control method names.
const (
	NotificationCatalogChanged = "catalog:changed"
	NotificationIndexProgress  = "index:progress"
	MethodCancelRequest        = "$/cancelRequest"
	MethodInitialize           = "initialize"
)

// Request represents an incoming JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// Notification represents a JSON-RPC 2.0 notification (no ID).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Capabilities indicates the features supported on the remote host.
type Capabilities struct {
	Trash  bool `json:"trash"`
	Manage bool `json:"manage"`
	Export bool `json:"export"`
	Import bool `json:"import"`
	Search bool `json:"search"`
}

// InitializeRequest is the first handshake message sent from client to server.
type InitializeRequest struct {
	ProtocolVersion int               `json:"protocolVersion"`
	AppVersion      string            `json:"appVersion"`
	ClientEnv       map[string]string `json:"clientEnv,omitempty"`
}

// InitializeResult is returned by the server on successful handshake.
type InitializeResult struct {
	ProtocolVersion int          `json:"protocolVersion"`
	AppVersion      string       `json:"appVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	Roots           paths.Roots  `json:"roots"`
}

// CancelParams configures the $/cancelRequest notification.
type CancelParams struct {
	ID any `json:"id"`
}

// Method parameter structs:

type PingParams struct {
	Name string `json:"name,omitempty"`
}

type ListGroupsParams struct {
	Mode   engine.GroupMode  `json:"mode"`
	Filter engine.FilterOpts `json:"filter"`
}

type ListSessionsParams struct {
	GroupKey string            `json:"groupKey,omitempty"`
	Filter   engine.FilterOpts `json:"filter"`
	Sort     engine.SortOpts   `json:"sort"`
}

type AgentCountsParams struct {
	Filter engine.FilterOpts `json:"filter"`
}

type GetSessionMetaParams struct {
	Ref model.SessionRef `json:"ref"`
}

type GetMessagesParams struct {
	Ref    model.SessionRef `json:"ref"`
	Offset int              `json:"offset"`
	Limit  int              `json:"limit"`
}

type GetBlobParams struct {
	Ref model.SessionRef `json:"ref"`
	Key string           `json:"key"`
}

type SearchParams struct {
	Query  string             `json:"query"`
	Filter index.SearchFilter `json:"filter"`
}

type CopyResumeCommandParams struct {
	Ref model.SessionRef `json:"ref"`
}

type RevealSourceParams struct {
	Ref model.SessionRef `json:"ref"`
}

type SetManageEnabledParams struct {
	Enabled bool `json:"enabled"`
}

type SetAllowPermanentDeleteParams struct {
	Allow bool `json:"allow"`
}

type PreviewDeleteParams struct {
	Refs []model.SessionRef `json:"refs"`
}

type DeleteSessionsParams struct {
	Refs  []model.SessionRef `json:"refs"`
	Token string             `json:"token"`
}

type BuildHandoffParams struct {
	Req engine.HandoffRequest `json:"req"`
}

type HandoffCommandParams struct {
	Req engine.HandoffRequest `json:"req"`
}

type SaveHandoffParams struct {
	Req      engine.HandoffRequest `json:"req"`
	DestPath string                `json:"destPath"`
}

type RenderHandoffParams struct {
	Req engine.HandoffRequest `json:"req"`
}

type PreviewExportParams struct {
	Req engine.ExportRequest `json:"req"`
}

type ExportBundleParams struct {
	Req      engine.ExportRequest `json:"req"`
	DestPath string               `json:"destPath"`
}

type OpenBundleParams struct {
	Path string `json:"path"`
}

type OpenBundleBytesParams struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type BuildBundleHandoffParams struct {
	Req engine.BundleHandoffRequest `json:"req"`
}

type BundleHandoffCommandParams struct {
	Req engine.BundleHandoffRequest `json:"req"`
}

type SaveBundleHandoffParams struct {
	Req      engine.BundleHandoffRequest `json:"req"`
	DestPath string                      `json:"destPath"`
}

type RenderBundleHandoffParams struct {
	Req engine.BundleHandoffRequest `json:"req"`
}
