package rpc

import (
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
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
	// MethodHeartbeat is a notification the client sends on a timer so the
	// server can tell a live but quiet client from one that vanished.
	MethodHeartbeat  = "$/heartbeat"
	MethodInitialize = "initialize"
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
	GitDiff bool `json:"gitDiff,omitempty"`
	// GitChanges supports repository status (and is absent on older servers).
	GitChanges bool `json:"gitChanges,omitempty"`
	GitWrite   bool `json:"gitWrite,omitempty"`
	// GitSync supports fetch/pull/push; servers only advertise it with GitWrite.
	GitSync             bool `json:"gitSync,omitempty"`
	CodeDiagnostics     bool `json:"codeDiagnostics,omitempty"`
	LiveCodeDiagnostics bool `json:"liveCodeDiagnostics,omitempty"`
	// CodeNavigation: definition, references and hover for live documents.
	// Only with LiveCodeDiagnostics.
	CodeNavigation bool `json:"codeNavigation,omitempty"`
	Trash          bool `json:"trash"`
	Manage         bool `json:"manage"`
	Export         bool `json:"export"`
	Import         bool `json:"import"`
	Search         bool `json:"search"`
	// SaveFile: the file viewer may save edited files. Older servers leave
	// it false, and their files stay read-only.
	SaveFile bool `json:"saveFile,omitempty"`
	// Blame supports on-demand blame for one saved working-tree line.
	Blame bool `json:"blame,omitempty"`
	// SearchFiles supports searching the text of a project's files.
	SearchFiles bool `json:"searchFiles,omitempty"`
	// ReplaceFiles supports replacing search matches in files. Only servers
	// that can save advertise it.
	ReplaceFiles bool `json:"replaceFiles,omitempty"`
	// NewSession supports newSessionTerminalScript and newSessionAgents:
	// starting a fresh agent session in a project.
	NewSession      bool `json:"newSession,omitempty"`
	AddFile         bool `json:"addFile,omitempty"`
	CreateDirectory bool `json:"createDirectory,omitempty"`
	CreateFile      bool `json:"createFile,omitempty"`
	RenameFile      bool `json:"renameFile,omitempty"`
	DeleteFile      bool `json:"deleteFile,omitempty"`
}

// InitializeRequest is the first handshake message sent from client to server.
type InitializeRequest struct {
	ProtocolVersion int               `json:"protocolVersion"`
	AppVersion      string            `json:"appVersion"`
	ClientEnv       map[string]string `json:"clientEnv,omitempty"`
}

// InitializeResult is returned by the server on successful handshake.
type InitializeResult struct {
	CodeDiagnosticsHostID string       `json:"codeDiagnosticsHostId,omitempty"`
	ProtocolVersion       int          `json:"protocolVersion"`
	AppVersion            string       `json:"appVersion"`
	Capabilities          Capabilities `json:"capabilities"`
	Roots                 paths.Roots  `json:"roots"`
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
	// Panel asks a terminal script method for the app's terminal panel,
	// whose script ends with the agent. Older servers ignore it.
	Panel bool `json:"panel,omitempty"`
}

// NewSessionParams asks for the script that starts a fresh session of Agent
// in Dir, a directory the server's catalog knows.
type NewSessionParams struct {
	Agent model.AgentID `json:"agent"`
	Dir   string        `json:"dir"`
	Panel bool          `json:"panel,omitempty"` // as in CopyResumeCommandParams
}

type RevealSourceParams struct {
	Ref model.SessionRef `json:"ref"`
}

type ListFilesParams struct {
	Dir string `json:"dir"`
}

type ProbeFilesParams struct {
	Dir   string   `json:"dir"`
	Paths []string `json:"paths"`
}

type ReadFileParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
}

type BlameFileParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
	Line int    `json:"line"`
}

type SearchFilesParams struct {
	Dir   string            `json:"dir"`
	Query files.SearchQuery `json:"query"`
}

type SaveFileParams struct {
	Dir     string            `json:"dir"`
	Request files.SaveRequest `json:"request"`
}

type ReplaceInFilesParams struct {
	Dir     string               `json:"dir"`
	Request files.ReplaceRequest `json:"request"`
}

type SetManageEnabledParams struct {
	Enabled bool `json:"enabled"`
}

type SetAllowPermanentDeleteParams struct {
	Allow bool `json:"allow"`
}

type SetPrunePromptHistoryParams struct {
	Prune bool `json:"prune"`
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
	Req   engine.HandoffRequest `json:"req"`
	Panel bool                  `json:"panel,omitempty"` // as in CopyResumeCommandParams
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
	Req   engine.BundleHandoffRequest `json:"req"`
	Panel bool                        `json:"panel,omitempty"` // as in CopyResumeCommandParams
}

type SaveBundleHandoffParams struct {
	Req      engine.BundleHandoffRequest `json:"req"`
	DestPath string                      `json:"destPath"`
}

type PreviewBundleRestoreParams struct {
	Req engine.BundleRestoreRequest `json:"req"`
}

type RestoreBundleParams struct {
	Req   engine.BundleRestoreRequest `json:"req"`
	Token string                      `json:"token"`
}

type RenderBundleHandoffParams struct {
	Req engine.BundleHandoffRequest `json:"req"`
}

type GitStatusParams struct {
	Dir string `json:"dir"`
}
