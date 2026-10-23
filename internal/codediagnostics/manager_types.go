package codediagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

const maxSequence int64 = 9007199254740991

// Context identifies one connection-owned result epoch, not execution approval.
type Context struct {
	ConnectionID string `json:"connectionId"`
	AttachmentID string `json:"attachmentId"`
	Generation   string `json:"generation"`
}
type Scope struct {
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
}
type ResultSet struct {
	ProviderID  string       `json:"providerId"`
	Origin      string       `json:"origin"`
	Scope       Scope        `json:"scope"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	RunID       string       `json:"runId,omitempty"`
}
type Coverage struct {
	Kind            string   `json:"kind"`
	Roots           []string `json:"roots"`
	Partial         bool     `json:"partial"`
	ExternalChanges string   `json:"externalChanges"`
}
type ProviderState struct {
	ProviderID   string   `json:"providerId"`
	Availability string   `json:"availability"`
	Phase        string   `json:"phase"`
	Coverage     Coverage `json:"coverage"`
	Freshness    string   `json:"freshness"`
	Truncated    bool     `json:"truncated"`
	RunID        string   `json:"runId,omitempty"`
	Notice       string   `json:"notice,omitempty"`
	Failure      *Error   `json:"failure,omitempty"`
	// DocumentExtensions lists the file extensions an openedDocuments provider
	// synchronizes (additive, contract v1). Clients open only matching paths, so
	// files no provider supports are "not live" rather than failed. The backend
	// still resolves the owner authoritatively on every open.
	DocumentExtensions []string `json:"documentExtensions,omitempty"`
}
type ProposalTool struct {
	ProviderID  string   `json:"providerId"`
	Executable  string   `json:"executable"`
	ConfigPaths []string `json:"configPaths"`
}

// ProposalAnalyzer is a backend-owned in-process analyzer covered by the
// approval. It executes nothing; it is listed so consent is explicit.
type ProposalAnalyzer struct {
	ProviderID  string `json:"providerId"`
	Description string `json:"description"`
}
type Proposal struct {
	Fingerprint string         `json:"fingerprint"`
	Tools       []ProposalTool `json:"tools"`
	// Analyzers lists in-process providers gated by this approval. When Tools
	// is empty the fingerprint is an analyzer-only identity (InProcessFingerprint)
	// that can never authorize an executable.
	Analyzers []ProposalAnalyzer `json:"analyzers,omitempty"`
}
type Snapshot struct {
	ContractVersion int             `json:"contractVersion"`
	Context         Context         `json:"context"`
	Sequence        int64           `json:"sequence"`
	Approval        Approval        `json:"approval"`
	Providers       []ProviderState `json:"providers"`
	ResultSets      []ResultSet     `json:"resultSets"`
	Proposal        *Proposal       `json:"proposal,omitempty"`
}
type Event struct {
	ContractVersion int      `json:"contractVersion"`
	Context         Context  `json:"context"`
	Sequence        int64    `json:"sequence"`
	UpdateID        string   `json:"updateId"`
	PartIndex       int      `json:"partIndex"`
	PartCount       int      `json:"partCount"`
	Snapshot        Snapshot `json:"snapshot"`
}
type RunIdentity struct {
	ProviderID string `json:"providerId"`
	RunID      string `json:"runId"`
}

// CheckerAdapter is trusted backend code, never decoded from a client request.
// Discovery is metadata-only. RunApproved must use the supplied complete approval
// tool set for every supervisor submission, and honor cancellation promptly.
// GoVetPlan and SveltePlan implement this seam; registration enables no transport.
type CheckerAdapter interface {
	Tools() []ToolInput
	RunApproved(context.Context, *Supervisor, string, []ToolInput) CheckerResult
}
type Registration struct {
	ProviderID   string
	Availability string
	Adapter      CheckerAdapter    // nil for unsupported/missing/disabled providers
	Live         *LiveRegistration // backend-owned metadata; never starts a session
	// InProcess marks a backend-owned analyzer that executes no tool (go-syntax).
	// It contributes nothing to the approval proposal, yet publishes only while
	// the attachment's project approval is current (PublishInProcess).
	InProcess bool
}

// Limits can only narrow contract v1. Metadata is deliberately limited further
// to leave room for full repeated metadata in at most 32 event envelopes.
type ManagerLimits struct {
	Attachments, Pending, Findings, FindingBytes, SnapshotBytes, EventBytes int
}

func (l ManagerLimits) defaults() ManagerLimits {
	clamp := func(n, max int) int {
		if n <= 0 || n > max {
			return max
		}
		return n
	}
	l.Attachments = clamp(l.Attachments, 8)
	l.Pending = clamp(l.Pending, 32)
	l.Findings = clamp(l.Findings, 10000)
	l.FindingBytes = clamp(l.FindingBytes, 7<<20)
	l.SnapshotBytes = clamp(l.SnapshotBytes, 8<<20)
	l.EventBytes = clamp(l.EventBytes, 1<<20)
	return l
}

func opaqueID() string {
	var b [16]byte
	// Failure of the OS random source is fatal in crypto/rand, not a reusable ID.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
func encodedSize(v any) int { b, _ := json.Marshal(v); return len(b) }
func copySnapshot(s Snapshot) Snapshot {
	b, _ := json.Marshal(s)
	var out Snapshot
	_ = json.Unmarshal(b, &out)
	return out
}
