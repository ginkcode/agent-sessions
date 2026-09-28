// Package opencode implements provider.Provider for OpenCode sessions,
// covering the v2 and v1 SQLite generations and the pre-DB legacy JSON tree.
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

// Scan, Load and Blob are implemented by M1-03 … M1-06; the stubs below keep
// the provider usable for detection until those tasks land. Replace them when
// M1-03 (scan), M1-04/05 (v1/legacy load) and M1-06 (blob) are implemented.

// Scan reports an unsupported error until M1-03 implements v2 scanning.
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	return provider.ScanResult{}, scanUnsupported(ctx)
}

// Load returns an unsupported error until M1-04 … M1-06 load transcripts.
func (p *Provider) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	return nil, loadUnsupported(ctx)
}

// Blob returns an unsupported error until M1-06 retrieves attachments.
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	return nil, blobUnsupported(ctx)
}

// ResumeCommand returns the OpenCode CLI invocation that continues m. The
// app quotes argv and cwd for a shell; the provider never builds shell text.
func (p *Provider) ResumeCommand(m model.SessionMeta) provider.Command {
	return provider.Command{
		Argv: []string{"opencode", "--session", m.Ref.ID},
		Dir:  m.CWD,
	}
}

// unsupportedErr describes a provider surface that a later M1 task owns.
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

func scanUnsupported(ctx context.Context) error {
	cause := ctx.Err()
	return &unsupportedErr{task: ErrUnsupportedScan.task, kind: ErrUnsupportedScan.kind, cause: cause}
}

func loadUnsupported(ctx context.Context) error {
	cause := ctx.Err()
	return &unsupportedErr{task: ErrUnsupportedLoad.task, kind: ErrUnsupportedLoad.kind, cause: cause}
}

func blobUnsupported(ctx context.Context) error {
	cause := ctx.Err()
	return &unsupportedErr{task: ErrUnsupportedBlob.task, kind: ErrUnsupportedBlob.kind, cause: cause}
}

// ErrUnsupportedScan, ErrUnsupportedLoad and ErrUnsupportedBlob let callers
// (and the temporary stub tests) recognize the not-yet-implemented surfaces
// until the later M1 tasks replace them.
var (
	// ErrUnsupportedScan marks the stubbed Scan implementation.
	ErrUnsupportedScan = &unsupportedErr{task: "M1-03", kind: "Scan"}
	// ErrUnsupportedLoad marks the stubbed Load implementation.
	ErrUnsupportedLoad = &unsupportedErr{task: "M1-04…M1-06", kind: "Load"}
	// ErrUnsupportedBlob marks the stubbed Blob implementation.
	ErrUnsupportedBlob = &unsupportedErr{task: "M1-06", kind: "Blob"}
)

func (e *unsupportedErr) Is(target error) bool {
	t, ok := target.(*unsupportedErr)
	if !ok {
		return false
	}
	return e.kind == t.kind && e.task == t.task
}
