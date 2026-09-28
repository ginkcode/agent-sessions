// Package provider defines the interface for scanning and loading agent sessions.
package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// Detection reports whether an agent's storage is present on this machine.
type Detection struct {
	Present     bool     `json:"present"`
	Roots       []string `json:"roots"`
	Generations []string `json:"generations,omitempty"` // e.g. OpenCode: "v2","v1","legacy"
	Notes       []string `json:"notes,omitempty"`
}

// SourceState is the provider's resume point for one source (file or DB).
type SourceState struct {
	Size       int64           `json:"size"`
	ModTimeNs  int64           `json:"modTimeNs"`
	Offset     int64           `json:"offset"`               // bytes fully consumed (JSONL)
	Checkpoint json.RawMessage `json:"checkpoint,omitempty"` // provider-private partial aggregate
}

// ScanState tracks scanner progress across sources.
type ScanState struct {
	Sources map[string]SourceState `json:"sources"`          // key: absolute path
	Cursor  string                 `json:"cursor,omitempty"` // DB providers (e.g. max time_updated)
}

// ScanResult reports sessions changed or removed since previous scan.
type ScanResult struct {
	Changed []model.SessionMeta `json:"changed"` // new or updated since prev; all sessions when prev is empty
	Removed []model.SessionRef  `json:"removed"` // present in prev, gone now
	State   ScanState           `json:"state"`
	Diag    Diagnostics         `json:"diag"`
}

// Command is an executable command line with working directory.
type Command struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
}

// Provider discovers and loads sessions for a specific agent.
type Provider interface {
	ID() model.AgentID
	DisplayName() string
	Detect(ctx context.Context) (Detection, error)
	Scan(ctx context.Context, prev ScanState) (ScanResult, error)
	Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error)
	Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error)
	WatchPaths() []string
	ResumeCommand(m model.SessionMeta) Command
}

// LiveDetector is an optional interface for providers supporting live session detection.
type LiveDetector interface {
	Live(ctx context.Context) (map[string]LiveInfo, error) // key: session ID
}

// LiveInfo describes the live status of an active session.
type LiveInfo struct {
	PID       int       `json:"pid"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Set is an ordered list of providers.
type Set []Provider

// Get returns the provider with the specified AgentID, if present.
func (s Set) Get(id model.AgentID) (Provider, bool) {
	for _, p := range s {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}
