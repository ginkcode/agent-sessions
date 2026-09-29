// Package bundle implements the portable session archive format (.agent-session.zip).
package bundle

import (
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// CurrentFormat is the manifest format identifier.
const CurrentFormat = "agent-sessions.bundle"

// CurrentVersion is the bundle manifest specification version.
const CurrentVersion = 1

// Profile selects the bundle privacy and completeness tier.
type Profile string

const (
	ProfileComplete  Profile = "complete"   // includes native records, restorable
	ProfileShareSafe Profile = "share-safe" // redacted, handoff-only, no native records
)

// Manifest is the root manifest.json inside an .agent-session.zip bundle.
type Manifest struct {
	Format     string            `json:"format"`
	Version    int               `json:"version"`
	CreatedAt  time.Time         `json:"createdAt"`
	AppVersion string            `json:"appVersion,omitempty"`
	Profile    Profile           `json:"profile"`
	Source     SourceMeta        `json:"source"`
	Sessions   []SessionManifest `json:"sessions"`
	Redaction  RedactionManifest `json:"redaction"`
	Handoff    string            `json:"handoff"` // path inside zip, typically "handoff.md"
}

// SourceMeta records the primary origin session details.
type SourceMeta struct {
	Agent        model.AgentID `json:"agent"`
	AgentVersion string        `json:"agentVersion,omitempty"`
	ID           string        `json:"id"`
	CWD          string        `json:"cwd"`
	RepoRoot     string        `json:"repoRoot,omitempty"`
	GitBranch    string        `json:"gitBranch,omitempty"`
	Title        string        `json:"title,omitempty"`
}

// SessionManifest maps a session (root or child) and its verbatim files.
type SessionManifest struct {
	Ref      model.SessionRef `json:"ref"`
	ParentID string           `json:"parentId,omitempty"`
	Native   []NativeFile     `json:"native,omitempty"`
}

// NativeFile documents one verbatim native storage file inside native/<agent>/...
type NativeFile struct {
	Name    string `json:"name"`    // zip-internal path: "native/<agent>/..."
	RootRel string `json:"rootRel"` // relative path within tool's storage root
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// RedactionManifest records redaction rule counts applied during bundle creation.
type RedactionManifest struct {
	Rules  map[string]int `json:"rules"`
	Counts int            `json:"counts"`
}
