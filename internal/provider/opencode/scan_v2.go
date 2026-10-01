package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

const v2BatchSize = 100

// v2Query runs against a single read-only transaction, so the ID sweep,
// timestamp, metadata and counts all observe the same SQLite snapshot.
type v2Query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func v2IDs(ctx context.Context, db v2Query) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM session_v2 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("v2 IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		if len(ids)%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("v2 ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("v2 IDs: %w", err)
	}
	return ids, nil
}

// v2Metas can select either rows past a cursor or a bounded list of new IDs.
// Each batch of message queries is parameterized and never reads attachment
// data or materializes assistant JSON in Go. hasProject must come from a
// schema probe on *sql.DB before this helper runs inside a transaction.
func (p *Provider) v2Metas(ctx context.Context, db v2Query, project bool, sinceMs int64, full bool, selectedIDs []string, d *provider.Diagnostics) ([]model.SessionMeta, error) {
	worktree := "NULL"
	join := ""
	if project {
		worktree = "p.worktree"
		join = " LEFT JOIN project AS p ON p.id = s.project_id"
	}
	query := `SELECT s.id, s.project_id, s.parent_id, s.directory, s.title, s.version,
		s.agent, s.model, s.cost, s.tokens_input, s.tokens_output,
		s.tokens_reasoning, s.tokens_cache_read, s.tokens_cache_write,
		s.time_created, s.time_updated, s.time_archived, ` + worktree + `
		FROM session_v2 AS s` + join
	var args []any
	if selectedIDs != nil {
		if len(selectedIDs) == 0 {
			return nil, nil
		}
		query += " WHERE s.id IN (" + placeholders(len(selectedIDs)) + ")"
		for _, id := range selectedIDs {
			args = append(args, id)
		}
	} else if !full {
		query += " WHERE s.time_updated >= ?"
		args = append(args, sinceMs)
	}
	query += " ORDER BY s.id"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("v2 session rows: %w", err)
	}
	var metas []model.SessionMeta
	origins := make(map[string]metaOrigin)
	for rows.Next() {
		if len(metas)%1000 == 0 {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		var id string
		var projectID, parent, dir, title, version, agent, rawModel, worktree sql.NullString
		var cost sql.NullFloat64
		var input, output, reasoning, cacheRead, cacheWrite, created, updated, archived sql.NullInt64
		if err := rows.Scan(&id, &projectID, &parent, &dir, &title, &version, &agent, &rawModel, &cost,
			&input, &output, &reasoning, &cacheRead, &cacheWrite, &created, &updated, &archived, &worktree); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("v2 session row: %w", err)
		}
		modelName, malformed := parseModelName(rawModel)
		if malformed {
			d.ParseErrors++
			d.Warn(p.dbPath(), 0, "session %q has invalid model JSON", id)
		}
		meta := model.SessionMeta{
			Ref:      model.SessionRef{Agent: model.AgentOpenCode, ID: id},
			ParentID: parent.String, SourcePath: p.dbPath(), CWD: dir.String,
			Title: model.OneLine(title.String), AgentVersion: version.String, AgentName: agent.String,
			Model: modelName, CreatedAt: unixMilli(created), UpdatedAt: unixMilli(updated),
			CostUSD: cost.Float64, Archived: archived.Valid,
			Tokens: model.TokenUsage{
				Input: input.Int64, Output: output.Int64, Reasoning: reasoning.Int64,
				CacheRead: cacheRead.Int64, CacheWrite: cacheWrite.Int64,
			},
		}
		origins[id] = metaOrigin{projectID: projectID.String, worktree: worktree}
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("v2 session rows: %w", err)
	}
	// The helper uses one SQLite connection. Close these rows before issuing
	// any of the short per-batch message queries.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("v2 session rows close: %w", err)
	}
	for start := 0; start < len(metas); start += v2BatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+v2BatchSize, len(metas))
		if err := p.v2Counts(ctx, db, metas[start:end], d); err != nil {
			return nil, err
		}
	}
	p.finishMetas(metas, origins, d)
	return metas, nil
}

func (p *Provider) v2Counts(ctx context.Context, db v2Query, metas []model.SessionMeta, d *provider.Diagnostics) error {
	args := make([]any, len(metas))
	byID := make(map[string]*model.SessionMeta, len(metas))
	for i := range metas {
		args[i] = metas[i].Ref.ID
		byID[metas[i].Ref.ID] = &metas[i]
	}
	in := placeholders(len(args))
	roleQuery := `SELECT session_id,
		SUM(CASE WHEN type='user' THEN 1 ELSE 0 END),
		SUM(CASE WHEN type='assistant' THEN 1 ELSE 0 END)
		FROM session_message WHERE session_id IN (` + in + `) AND type IN ('user','assistant') GROUP BY session_id`
	rows, err := db.QueryContext(ctx, roleQuery, args...)
	if err != nil {
		return fmt.Errorf("v2 role counts: %w", err)
	}
	for rows.Next() {
		var id string
		var users, assistants sql.NullInt64
		if err := rows.Scan(&id, &users, &assistants); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v2 role counts: %w", err)
		}
		if meta := byID[id]; meta != nil {
			meta.Counts.User, meta.Counts.Assistant = int(users.Int64), int(assistants.Int64)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v2 role counts: %w", err)
	}

	// CASE guards json_each even on SQLite versions where a WHERE predicate
	// might be reordered. A non-object content item must not abort the batch.
	toolQuery := `SELECT m.session_id, COUNT(*) FROM session_message AS m,
		json_each(CASE WHEN json_valid(m.data) THEN m.data ELSE '{}' END, '$.content') AS item
		WHERE m.session_id IN (` + in + `) AND m.type='assistant'
		AND CASE WHEN item.type='object' AND json_valid(item.value)
			THEN json_extract(item.value, '$.type')='tool' ELSE 0 END
		GROUP BY m.session_id`
	rows, err = db.QueryContext(ctx, toolQuery, args...)
	if err != nil {
		return fmt.Errorf("v2 tool counts: %w", err)
	}
	for rows.Next() {
		var id string
		var tools int
		if err := rows.Scan(&id, &tools); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v2 tool counts: %w", err)
		}
		if meta := byID[id]; meta != nil {
			meta.Counts.ToolCalls = tools
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v2 tool counts: %w", err)
	}

	invalidQuery := `SELECT session_id, id FROM session_message
		WHERE session_id IN (` + in + `) AND type IN ('assistant','user') AND COALESCE(json_valid(data),0)=0`
	rows, err = db.QueryContext(ctx, invalidQuery, args...)
	if err != nil {
		return fmt.Errorf("v2 invalid message JSON: %w", err)
	}
	for rows.Next() {
		var sessionID, messageID string
		if err := rows.Scan(&sessionID, &messageID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v2 invalid message JSON: %w", err)
		}
		d.ParseErrors++
		d.Warn(p.dbPath(), 0, "invalid JSON in session %q message %q", sessionID, messageID)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v2 invalid message JSON: %w", err)
	}

	unknownQuery := `SELECT type, COUNT(*) FROM session_message WHERE session_id IN (` + in + `)
		AND (type IS NULL OR type NOT IN ('user','assistant','compaction','synthetic','system','idle','location-switched'))
		GROUP BY type`
	rows, err = db.QueryContext(ctx, unknownQuery, args...)
	if err != nil {
		return fmt.Errorf("v2 message types: %w", err)
	}
	for rows.Next() {
		var kind sql.NullString
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v2 message types: %w", err)
		}
		name := kind.String
		if !kind.Valid {
			name = "<null>"
		}
		for i := 0; i < count; i++ {
			d.Unknown(name)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v2 message types: %w", err)
	}

	// The first user row by seq defines the preview, even if its JSON is
	// invalid. SQLite extracts only text, never files.data/base64.
	// ROW_NUMBER keeps this one query per batch instead of one per session.
	promptQuery := `SELECT session_id, text FROM (
		SELECT m.session_id,
			CASE WHEN json_valid(m.data) AND json_type(m.data,'$.text')='text'
				THEN json_extract(m.data,'$.text') END AS text,
			ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.seq) AS rn
		FROM session_message AS m
		WHERE m.session_id IN (` + in + `) AND m.type='user'
	) WHERE rn=1`
	rows, err = db.QueryContext(ctx, promptQuery, args...)
	if err != nil {
		return fmt.Errorf("v2 first prompts: %w", err)
	}
	for rows.Next() {
		var id string
		var text sql.NullString
		if err := rows.Scan(&id, &text); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v2 first prompts: %w", err)
		}
		if meta := byID[id]; meta != nil {
			meta.FirstPrompt = model.TruncateRunes(model.OneLine(text.String), 300)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v2 first prompts: %w", err)
	}

	// Naming uses the latest user message with text while FirstPrompt remains
	// the stable session-opening preview used by handoff and search.
	titleQuery := `SELECT session_id, text FROM (
		SELECT m.session_id, json_extract(m.data,'$.text') AS text,
			ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.seq DESC, m.id DESC) AS rn
		FROM session_message AS m
		WHERE m.session_id IN (` + in + `) AND m.type='user'
			AND CASE WHEN json_valid(m.data) AND json_type(m.data,'$.text')='text'
				THEN trim(json_extract(m.data,'$.text'), ' '||char(9,10,13))!='' ELSE 0 END
	) WHERE rn=1`
	rows, err = db.QueryContext(ctx, titleQuery, args...)
	if err != nil {
		return fmt.Errorf("v2 latest user prompts: %w", err)
	}
	for rows.Next() {
		var id string
		var text sql.NullString
		if err := rows.Scan(&id, &text); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v2 latest user prompts: %w", err)
		}
		if meta := byID[id]; meta != nil && meta.Title == "" {
			meta.Title = model.TruncateRunes(model.OneLine(text.String), 300)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v2 latest user prompts: %w", err)
	}

	return nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return err
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func unixMilli(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.UnixMilli(value.Int64).UTC()
}

func parseModelName(raw sql.NullString) (string, bool) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" || strings.TrimSpace(raw.String) == "null" {
		return "", false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw.String), &fields); err != nil || fields == nil {
		return "", true
	}
	var id, providerID string
	if value, ok := fields["providerID"]; ok && string(value) != "null" {
		if err := json.Unmarshal(value, &providerID); err != nil {
			// providerID is parsed first so a malformed id below still keeps
			// the parsed provider part.
			return "", true
		}
	}
	if value, ok := fields["id"]; ok && string(value) != "null" {
		if err := json.Unmarshal(value, &id); err != nil {
			// providerID was parsed above and stays even when id is malformed.
			return providerID, true
		}
	}
	if id == "" {
		return providerID, false
	}
	if providerID == "" {
		return id, false
	}
	return providerID + "/" + id, false
}
