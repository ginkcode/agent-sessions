package codex

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// These identifiers are constants from our schema allowlist, never SQL from a
// DB row. Older Codex indexes can omit any of these except the thread ID.
var threadColumns = []string{
	"id", "rollout_path", "cwd", "title", "first_user_message", "preview",
	"model", "tokens_used", "archived", "git_branch", "cli_version",
	"created_at_ms", "updated_at_ms", "created_at", "updated_at",
}

type indexedThread struct {
	meta            model.SessionMeta
	path            string
	archivedPresent bool
}

type threadIndex struct {
	threads map[string]indexedThread
	edges   map[string]string // child ID -> parent ID
	// If the edges table exists, its relationships supersede rollout ancestry.
	hasEdges bool
}

// readIndex is advisory: an unavailable/malformed index must not hide files.
// Cancellation remains fatal, so a canceled scan cannot commit partial state.
func (p *Provider) readIndex(ctx context.Context, diag *provider.Diagnostics) threadIndex {
	index := threadIndex{threads: make(map[string]indexedThread)}
	path, err := p.indexDBPath()
	if err != nil {
		diag.Warn(p.root, 0, "Codex threads index unavailable: %v; using rollouts", err)
		return index
	}
	if path == "" {
		diag.Warn(p.root, 0, "Codex threads index missing; using rollouts")
		return index
	}

	loaded, err := p.loadIndex(ctx, path)
	if err != nil {
		if ctx.Err() == nil {
			diag.Warn(path, 0, "Codex threads index unavailable: %v; using rollouts", err)
		}
		return index
	}
	if len(loaded.threads) == 0 {
		diag.Warn(path, 0, "Codex threads index empty; using rollouts")
	}
	return loaded
}

func (p *Provider) loadIndex(ctx context.Context, path string) (threadIndex, error) {
	index := threadIndex{threads: make(map[string]indexedThread), edges: make(map[string]string)}
	db, err := sqliteread.Open(ctx, path)
	if err != nil {
		return index, err
	}
	defer func() { _ = db.Close() }()

	exists, err := sqliteread.HasTable(ctx, db, "threads")
	if err != nil {
		return index, fmt.Errorf("threads table in %q: %w", path, err)
	}
	if !exists {
		return index, fmt.Errorf("threads table missing from %q", path)
	}
	columns, err := sqliteread.Columns(ctx, db, "threads")
	if err != nil {
		return index, fmt.Errorf("threads columns in %q: %w", path, err)
	}
	if !columns["id"] {
		return index, fmt.Errorf("threads ID column missing from %q", path)
	}
	fields := make([]string, len(threadColumns))
	for i, name := range threadColumns {
		if columns[name] {
			fields[i] = `"` + name + `"`
		} else {
			fields[i] = `NULL AS "` + name + `"`
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT "+strings.Join(fields, ", ")+` FROM "threads" ORDER BY "id"`)
	if err != nil {
		return index, fmt.Errorf("query threads in %q: %w", path, err)
	}
	for rows.Next() {
		var id, rollout, cwd, title, first, preview, modelName, branch, version sql.NullString
		var tokens, archived, createdMs, updatedMs, createdSec, updatedSec sql.NullInt64
		err = rows.Scan(&id, &rollout, &cwd, &title, &first, &preview, &modelName,
			&tokens, &archived, &branch, &version, &createdMs, &updatedMs, &createdSec, &updatedSec)
		if err != nil {
			break
		}
		if id.String == "" {
			continue
		}
		meta := model.SessionMeta{
			Ref:          model.SessionRef{Agent: model.AgentCodex, ID: id.String},
			CWD:          pathutil.NormalizeDir(cwd.String),
			Title:        title.String,
			FirstPrompt:  model.TruncateRunes(model.OneLine(first.String), 300),
			Model:        modelName.String,
			GitBranch:    branch.String,
			AgentVersion: version.String,
		}
		if meta.FirstPrompt == "" {
			meta.FirstPrompt = model.TruncateRunes(model.OneLine(preview.String), 300)
		}
		if archived.Valid {
			meta.Archived = archived.Int64 != 0
		}
		meta.CreatedAt = indexedTime(createdMs, createdSec)
		meta.UpdatedAt = indexedTime(updatedMs, updatedSec)
		index.threads[id.String] = indexedThread{meta: meta, path: rollout.String, archivedPresent: archived.Valid}
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close() // release the single SQLite connection before querying edges
	if err != nil {
		return threadIndex{}, fmt.Errorf("read threads in %q: %w", path, err)
	}
	if closeErr != nil {
		return threadIndex{}, fmt.Errorf("close threads in %q: %w", path, closeErr)
	}

	exists, err = sqliteread.HasTable(ctx, db, "thread_spawn_edges")
	if err != nil {
		return threadIndex{}, fmt.Errorf("spawn edges in %q: %w", path, err)
	}
	if !exists {
		return index, nil // older index without an ancestry table
	}
	index.hasEdges = true
	edgeColumns, err := sqliteread.Columns(ctx, db, "thread_spawn_edges")
	if err != nil {
		return threadIndex{}, fmt.Errorf("spawn edge columns in %q: %w", path, err)
	}
	if !edgeColumns["parent_thread_id"] || !edgeColumns["child_thread_id"] {
		return threadIndex{}, fmt.Errorf("spawn edge columns missing from %q", path)
	}
	edgeRows, err := db.QueryContext(ctx, `SELECT parent_thread_id, child_thread_id FROM thread_spawn_edges ORDER BY child_thread_id`)
	if err != nil {
		return threadIndex{}, fmt.Errorf("query spawn edges in %q: %w", path, err)
	}
	for edgeRows.Next() {
		var parent, child sql.NullString
		if err = edgeRows.Scan(&parent, &child); err != nil {
			break
		}
		if parent.String != "" && child.String != "" {
			index.edges[child.String] = parent.String
		}
	}
	if err == nil {
		err = edgeRows.Err()
	}
	closeErr = edgeRows.Close()
	if err != nil {
		return threadIndex{}, fmt.Errorf("read spawn edges in %q: %w", path, err)
	}
	if closeErr != nil {
		return threadIndex{}, fmt.Errorf("close spawn edges in %q: %w", path, closeErr)
	}
	return index, nil
}

func indexedTime(ms, sec sql.NullInt64) time.Time {
	if ms.Valid {
		return time.UnixMilli(ms.Int64).UTC()
	}
	if sec.Valid {
		return time.Unix(sec.Int64, 0).UTC()
	}
	return time.Time{}
}

// indexRolloutPath accepts only an existing, regular JSONL rollout contained
// lexically AND physically inside the configured root. No DB-supplied path is
// passed to a file scanner before this check (including absolute paths and
// symlink escapes). Relative paths are relative to the Codex root.
func (p *Provider) indexRolloutPath(raw string) (string, error) {
	if raw == "" {
		return "", nil // older rows can be matched by ID to discovered files
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(p.root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if _, ok := lexicallyWithin(p.root, candidate); !ok {
		rel, ok := lexicallyWithin(p.configured, candidate)
		if !ok {
			return "", fmt.Errorf("rollout_path %q is outside Codex root", raw)
		}
		// Written under the root as configured (a symlinked ~/.codex, or a
		// short name on Windows): use the resolved root's form, which is the
		// path discovery reports for the same file.
		candidate = filepath.Join(p.root, rel)
	}
	name := filepath.Base(candidate)
	if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
		return "", fmt.Errorf("rollout_path %q is not a rollout JSONL file", raw)
	}
	if !withinRoot(p.root, candidate) {
		return "", fmt.Errorf("rollout_path %q is missing or escapes Codex root", raw)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("stat rollout_path %q: %w", raw, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("rollout_path %q is not a regular file", raw)
	}
	f, err := os.Open(candidate)
	if err != nil {
		return "", fmt.Errorf("open rollout_path %q: %w", raw, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close rollout_path %q: %w", raw, err)
	}
	return candidate, nil
}

// lexicallyWithin returns path relative to root when path is root or below it.
func lexicallyWithin(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// mergeIndexedMeta gives nonempty index metadata precedence, while retaining
// parsed conversation counts, directional token usage and rollout-only fields.
func (p *Provider) mergeIndexedMeta(file, indexed model.SessionMeta) model.SessionMeta {
	if indexed.Title != "" {
		file.Title = indexed.Title
	}
	if indexed.FirstPrompt != "" {
		file.FirstPrompt = indexed.FirstPrompt
	}
	if indexed.CWD != "" {
		file.CWD = indexed.CWD
		file.CWDMissing = !pathutil.Exists(file.CWD)
		file.RepoRoot = ""
		if p.git != nil {
			if repo, ok := p.git.Resolve(file.CWD); ok {
				file.RepoRoot = repo.MainRoot
			}
		}
	}
	if indexed.Model != "" {
		file.Model = indexed.Model
	}
	if indexed.AgentVersion != "" {
		file.AgentVersion = indexed.AgentVersion
	}
	if indexed.GitBranch != "" {
		file.GitBranch = indexed.GitBranch
	}
	if !indexed.CreatedAt.IsZero() {
		file.CreatedAt = indexed.CreatedAt
	}
	if !indexed.UpdatedAt.IsZero() {
		file.UpdatedAt = indexed.UpdatedAt
	}
	file.Archived = indexed.Archived
	return file
}
