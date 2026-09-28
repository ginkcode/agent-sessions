// Package opencode implements provider.Provider for OpenCode sessions,
// covering the v2 and v1 SQLite generations. The pre-DB legacy JSON tree is
// detected and watched but not yet read (M1-06); Scan warns when it is the
// only session store.
// Detection is strictly read-only and never touches auth.json or any
// credential/account/control_account table.
package opencode

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Compile-time check that the OpenCode provider satisfies provider.Provider.
var _ provider.Provider = (*Provider)(nil)

// Generation identifiers reported in provider.Detection.Generations.
const (
	// GenV2 is the SQLite generation backed by session_v2 and session_message.
	GenV2 = "v2"
	// GenV1 is the SQLite generation backed by session, message and part.
	GenV1 = "v1"
	// GenLegacy is the pre-DB storage/*.json tree.
	GenLegacy = "legacy"
)

// Store-relative paths. These are plain file names inside the OpenCode root;
// credentials (auth.json) are deliberately absent from every path built here.
const (
	dbName        = "opencode.db"
	storageSubdir = "storage"
	legacySession = "session"
	legacyMessage = "message"
	legacyPart    = "part"
)

// Provider implements provider.Provider for OpenCode sessions.
type Provider struct {
	root string // paths.Roots.OpenCodeData (e.g. ~/.local/share/opencode)
	git  *pathutil.GitResolver
}

// New constructs an OpenCode session provider rooted at root.
func New(root string, git *pathutil.GitResolver) *Provider {
	if git == nil {
		git = pathutil.NewGitResolver()
	}
	// SessionMeta.SourcePath and ScanState.Sources use absolute DB paths even
	// when the caller supplied a relative root.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Provider{
		root: filepath.Clean(root),
		git:  git,
	}
}

// ID returns the model.AgentOpenCode identifier.
func (p *Provider) ID() model.AgentID {
	return model.AgentOpenCode
}

// DisplayName returns the human-readable provider name.
func (p *Provider) DisplayName() string {
	return "OpenCode"
}

// dbPath is the absolute path of the SQLite store under the root.
func (p *Provider) dbPath() string {
	return filepath.Join(p.root, dbName)
}

// ResumeCommand returns the OpenCode CLI invocation that continues m. The
// app quotes argv and cwd for a shell; the provider never builds shell text.
func (p *Provider) ResumeCommand(m model.SessionMeta) provider.Command {
	return provider.Command{
		Argv: []string{"opencode", "--session", m.Ref.ID},
		Dir:  m.CWD,
	}
}

// unsupportedErr describes a provider surface that a later task owns.
type unsupportedErr struct {
	task  string
	kind  string
	cause error
}

func (e *unsupportedErr) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("opencode %s is not implemented yet (planned for %s): %v", e.kind, e.task, e.cause)
	}
	return fmt.Sprintf("opencode %s is not implemented yet (planned for %s)", e.kind, e.task)
}

func (e *unsupportedErr) Unwrap() error { return e.cause }

func loadUnsupported(ctx context.Context) error {
	cause := ctx.Err()
	return &unsupportedErr{task: ErrUnsupportedLoad.task, kind: ErrUnsupportedLoad.kind, cause: cause}
}

func blobUnsupported(ctx context.Context) error {
	cause := ctx.Err()
	return &unsupportedErr{task: ErrUnsupportedBlob.task, kind: ErrUnsupportedBlob.kind, cause: cause}
}

// ErrUnsupportedLoad and ErrUnsupportedBlob let callers recognize the
// surfaces that only the legacy JSON reader (M1-06) will provide: loading
// without a SQLite session schema, and legacy blob keys.
var (
	// ErrUnsupportedLoad marks Load without a readable SQLite generation.
	ErrUnsupportedLoad = &unsupportedErr{task: "M1-06", kind: "Load"}
	// ErrUnsupportedBlob marks blob keys outside the v1/v2 formats.
	ErrUnsupportedBlob = &unsupportedErr{task: "M1-06", kind: "Blob"}
)

func (e *unsupportedErr) Is(target error) bool {
	t, ok := target.(*unsupportedErr)
	if !ok {
		return false
	}
	return e.kind == t.kind && e.task == t.task
}
