// Package model defines the shared representation of sessions and transcripts.
package model

import (
	"encoding/json"
	"time"
)

// AgentID identifies a coding agent that owns a session.
type AgentID string

const (
	AgentClaude   AgentID = "claude-code"
	AgentCodex    AgentID = "codex"
	AgentOpenCode AgentID = "opencode"
)

// SessionRef uniquely identifies a session across providers.
type SessionRef struct {
	Agent AgentID `json:"agent"`
	ID    string  `json:"id"`
}

// Key returns the catalog key for a session.
func (r SessionRef) Key() string { return string(r.Agent) + ":" + r.ID }

// TokenUsage is the per-session or per-message token breakdown.
type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Add combines two usage reports.
func (t TokenUsage) Add(o TokenUsage) TokenUsage {
	return TokenUsage{
		Input:      t.Input + o.Input,
		Output:     t.Output + o.Output,
		Reasoning:  t.Reasoning + o.Reasoning,
		CacheRead:  t.CacheRead + o.CacheRead,
		CacheWrite: t.CacheWrite + o.CacheWrite,
	}
}

// MessageCounts counts conversational messages separately from tool calls.
type MessageCounts struct {
	User      int `json:"user"`
	Assistant int `json:"assistant"`
	ToolCalls int `json:"toolCalls"`
}

// Total is the number of user and assistant messages in the conversation.
func (c MessageCounts) Total() int { return c.User + c.Assistant }

// SessionMeta is the searchable summary of a provider session.
type SessionMeta struct {
	Ref              SessionRef    `json:"ref"`
	ParentID         string        `json:"parentId,omitempty"`
	ParentToolCallID string        `json:"parentToolCallId,omitempty"`
	SourcePath       string        `json:"sourcePath"`
	CWD              string        `json:"cwd"`
	RepoRoot         string        `json:"repoRoot,omitempty"`
	CWDMissing       bool          `json:"cwdMissing,omitempty"`
	GitBranch        string        `json:"gitBranch,omitempty"`
	Title            string        `json:"title"`
	FirstPrompt      string        `json:"firstPrompt,omitempty"`
	Model            string        `json:"model,omitempty"`
	AgentName        string        `json:"agentName,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	Counts           MessageCounts `json:"counts"`
	Tokens           TokenUsage    `json:"tokens"`
	CostUSD          float64       `json:"costUsd,omitempty"`
	Archived         bool          `json:"archived,omitempty"`
	Live             bool          `json:"live,omitempty"`
	LiveStatus       string        `json:"liveStatus,omitempty"`
	AgentVersion     string        `json:"agentVersion,omitempty"`
}

// Role identifies who authored a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// PartKind identifies the content of a message part.
type PartKind string

const (
	PartText       PartKind = "text"
	PartReasoning  PartKind = "reasoning"
	PartTool       PartKind = "tool"
	PartPatch      PartKind = "patch"
	PartFile       PartKind = "file"
	PartCompaction PartKind = "compaction"
	PartNotice     PartKind = "notice"
)

// ToolStatus describes whether a tool call has completed.
type ToolStatus string

const (
	ToolPending   ToolStatus = "pending"
	ToolCompleted ToolStatus = "completed"
	ToolError     ToolStatus = "error"
	ToolUnknown   ToolStatus = "unknown"
)

// ToolCall combines a call and its result into one part.
type ToolCall struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Input           json.RawMessage `json:"input,omitempty" ts_type:"any"`
	Output          string          `json:"output,omitempty"`
	OutputTruncated bool            `json:"outputTruncated,omitempty"`
	OutputRef       string          `json:"outputRef,omitempty"`
	Status          ToolStatus      `json:"status"`
	Child           *SessionRef     `json:"child,omitempty"`
}

// FileRef identifies attachment content that is loaded on demand.
type FileRef struct {
	Name string `json:"name,omitempty"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
	Ref  string `json:"ref"`
}

// Part is a displayable element within a message.
type Part struct {
	Kind  PartKind  `json:"kind"`
	Text  string    `json:"text,omitempty"`
	Tool  *ToolCall `json:"tool,omitempty"`
	File  *FileRef  `json:"file,omitempty"`
	Files []string  `json:"files,omitempty"`
}

// Message is one conversational turn; tools and reasoning live in its parts.
type Message struct {
	ID          string     `json:"id"`
	Role        Role       `json:"role"`
	Time        time.Time  `json:"time"`
	Model       string     `json:"model,omitempty"`
	Parts       []Part     `json:"parts"`
	IsMeta      bool       `json:"isMeta,omitempty"`
	IsSidechain bool       `json:"isSidechain,omitempty"`
	Tokens      TokenUsage `json:"tokens"`
}

// Transcript contains a session summary and its messages in source order.
type Transcript struct {
	Meta     SessionMeta `json:"meta"`
	Messages []Message   `json:"messages"`
}
