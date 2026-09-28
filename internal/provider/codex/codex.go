// Package codex implements the provider for OpenAI Codex CLI sessions
// stored as JSONL rollouts under $CODEX_HOME (or ~/.codex).
package codex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// Provider implements provider.Provider for Codex CLI sessions.
type Provider struct {
	root string // ~/.codex or custom root
	git  *pathutil.GitResolver
}

// New constructs a Codex session provider.
func New(root string, git *pathutil.GitResolver) *Provider {
	if git == nil {
		git = pathutil.NewGitResolver()
	}
	// The caller normally supplies paths.Default().Codex. A relative explicit
	// root is made absolute so discovery always returns absolute source paths.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Provider{
		root: pathutil.NormalizeDir(root),
		git:  git,
	}
}

// ID returns the model.AgentCodex identifier.
func (p *Provider) ID() model.AgentID {
	return model.AgentCodex
}

// DisplayName returns the human-readable provider name.
func (p *Provider) DisplayName() string {
	return "Codex"
}

// Detect checks whether Codex is installed and has session data. Present is
// true when any rollout files or a populated threads index exist; a missing
// root is normal and yields Present=false with no error.
func (p *Provider) Detect(ctx context.Context) (provider.Detection, error) {
	if err := ctx.Err(); err != nil {
		return provider.Detection{}, err
	}
	info, err := os.Stat(p.root)
	if err != nil {
		if os.IsNotExist(err) {
			return provider.Detection{Present: false, Roots: []string{p.root}}, nil
		}
		return provider.Detection{}, fmt.Errorf("codex: stat root %q: %w", p.root, err)
	}
	if !info.IsDir() {
		return provider.Detection{Present: false, Roots: []string{p.root}}, nil
	}

	files, err := p.discover(ctx)
	if err != nil {
		return provider.Detection{}, err
	}

	det := provider.Detection{
		Present: len(files) > 0,
		Roots:   []string{p.root},
	}

	// Probe only the highest state_<N>.sqlite and only the threads table.
	// M1-11 will use this index for metadata; an empty index alone does not
	// mean there are sessions. A corrupt/unavailable index cannot hide rollouts.
	index, err := p.indexDBPath()
	if err != nil {
		return provider.Detection{}, err
	}
	if index != "" {
		det.Generations = append(det.Generations, fmt.Sprintf("state:%s", filepath.Base(index)))
		det.Notes = append(det.Notes, "Codex threads index: "+index)
		db, err := sqliteread.Open(ctx, index)
		if err == nil {
			var exists bool
			exists, err = sqliteread.HasTable(ctx, db, "threads")
			if err == nil && exists {
				var one int
				err = db.QueryRowContext(ctx, "SELECT 1 FROM threads LIMIT 1").Scan(&one)
				if err == nil {
					det.Present = true
				} else if errors.Is(err, sql.ErrNoRows) {
					err = nil // empty threads table
				}
			}
			if closeErr := db.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return provider.Detection{}, ctx.Err()
			}
			det.Notes = append(det.Notes, fmt.Sprintf("Codex threads index %q unavailable: %v", index, err))
		}
	}

	return det, nil
}

// WatchPaths returns directory and file paths to monitor for changes.
func (p *Provider) WatchPaths() []string {
	watch := []string{
		filepath.Join(p.root, "sessions"),
		filepath.Join(p.root, "archived_sessions"),
	}
	if index, err := p.indexDBPath(); err == nil && index != "" {
		watch = append(watch, index, index+"-wal")
	}
	return watch
}

// ResumeCommand generates the command to resume a Codex session in a terminal.
// `codex resume` (verified on CLI 0.156.1) accepts the session UUID.
func (p *Provider) ResumeCommand(m model.SessionMeta) provider.Command {
	return provider.Command{
		Argv: []string{"codex", "resume", m.Ref.ID},
		Dir:  m.CWD,
	}
}

// Scan parses rollout metadata. Implemented in task M1-10 / M1-11.
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	return provider.ScanResult{}, notImplemented("Scan")
}

// Load builds a transcript from a rollout file. Implemented in task M1-12.
func (p *Provider) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	return nil, notImplemented("Load")
}

// Blob retrieves large tool output or attachment bytes on demand.
// Implemented in task M1-12.
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	return nil, notImplemented("Blob")
}

// errNotImplemented marks provider methods deferred to later tasks.
type errNotImplemented struct {
	method string
}

func (e *errNotImplemented) Error() string {
	return fmt.Sprintf("codex: %s is not implemented yet (M1-10/M1-12)", e.method)
}

func notImplemented(method string) error {
	return &errNotImplemented{method: method}
}

// Provider satisfies the full provider contract; Scan/Load/Blob are stubs
// until M1-10/M1-12 land.
var _ provider.Provider = (*Provider)(nil)
