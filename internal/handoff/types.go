package handoff

import (
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/redact"
)

// Budget presets in token counts (estimated as characters / 4).
const (
	BudgetCompact   = 25000
	BudgetDetailed  = 80000 // default
	BudgetFull      = 150000
	BudgetUnlimited = 0
)

// Options configures the handoff generator.
type Options struct {
	TargetAgent      model.AgentID `json:"targetAgent"`
	BudgetTokens     int           `json:"budgetTokens"` // 0 = unlimited
	IncludeReasoning bool          `json:"includeReasoning"`
	RedactSecrets    bool          `json:"redactSecrets"`
	RemappedCWD      string        `json:"remappedCwd,omitempty"`
	ContextFilePath  string        `json:"contextFilePath,omitempty"` // if set, referenced in preamble/closing
}

// Report details the generation outcome, trimming, and secret detection.
type Report struct {
	EstimatedTokens int           `json:"estimatedTokens"`
	BudgetTokens    int           `json:"budgetTokens"`
	Trimmed         bool          `json:"trimmed"`
	DroppedItems    []string      `json:"droppedItems"`
	RedactionCounts redact.Counts `json:"redactionCounts"`
}

// Doc contains the generated markdown deliverables.
type Doc struct {
	PromptMarkdown string `json:"promptMarkdown"` // Sections 1-5, 7, 8 (or trimmed full doc)
	FullMarkdown   string `json:"fullMarkdown"`   // Untrimmed full context document (sections 1-8)
	Report         Report `json:"report"`
}
