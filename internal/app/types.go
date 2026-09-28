// Package app implements the application service and desktop bindings.
package app

import (
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// GroupMode selects the order of the directory and agent group levels.
type GroupMode string

const (
	GroupModeDirAgent GroupMode = "dir-agent"
	GroupModeAgentDir GroupMode = "agent-dir"
	GroupModeFlat     GroupMode = "flat"
)

// FilterOpts narrows the sessions visible to the frontend.
type FilterOpts struct {
	Agent        string `json:"agent,omitempty"`
	Query        string `json:"query,omitempty"`
	LiveOnly     bool   `json:"liveOnly,omitempty"`
	Archived     bool   `json:"archived,omitempty"`
	HasSubagents bool   `json:"hasSubagents,omitempty"`
}

// SortOpts selects the ordering of the session list.
type SortOpts struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// GroupNode is a group or session in the navigation tree returned to the UI.
type GroupNode struct {
	Key          string             `json:"key"`
	Label        string             `json:"label"`
	Kind         string             `json:"kind,omitempty"`
	Secondary    string             `json:"secondary,omitempty"`
	Agent        string             `json:"agent,omitempty"`
	CWD          string             `json:"cwd,omitempty"`
	CWDMissing   bool               `json:"cwdMissing,omitempty"`
	SessionCount int                `json:"sessionCount"`
	Children     []GroupNode        `json:"children,omitempty"`
	Sessions     []model.SessionRef `json:"sessions,omitempty"`
}

// MessagesPage is one page of a session transcript.
type MessagesPage struct {
	Messages   []model.Message `json:"messages"`
	Offset     int             `json:"offset"`
	Limit      int             `json:"limit"`
	TotalCount int             `json:"totalCount"`
	HasMore    bool            `json:"hasMore"`
}

// BlobResponse is provider-attached content, returned as text or base64.
type BlobResponse struct {
	Data     string `json:"data"`
	Mime     string `json:"mime"`
	IsBinary bool   `json:"isBinary"`
}

// DiagnosticsSnapshot is the aggregated, provider-keyed scanner health report.
type DiagnosticsSnapshot struct {
	Providers map[string]provider.Diagnostics `json:"providers"`
	Errors    map[string]string               `json:"errors,omitempty"`
	States    map[string]provider.ScanState   `json:"states,omitempty"`
}
