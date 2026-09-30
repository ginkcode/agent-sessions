package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
)

// GroupMode aliases engine.GroupMode.
type GroupMode = engine.GroupMode

const (
	GroupModeDirAgent = engine.GroupModeDirAgent
	GroupModeAgentDir = engine.GroupModeAgentDir
	GroupModeFlat     = engine.GroupModeFlat
)

// DTO aliases to internal/engine.
type (
	FilterOpts          = engine.FilterOpts
	SortOpts            = engine.SortOpts
	GroupNode           = engine.GroupNode
	MessagesPage        = engine.MessagesPage
	BlobResponse        = engine.BlobResponse
	DiagnosticsSnapshot = engine.DiagnosticsSnapshot
)

func flattenGroupNodes(nodes []GroupNode) []GroupNode {
	return engine.FlattenGroupNodes(nodes)
}
