package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func v1IDs(ctx context.Context, db v2Query) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM session ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("v1 IDs: %w", err)
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
			return nil, fmt.Errorf("v1 ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("v1 IDs: %w", err)
	}
	return ids, nil
}

// v1Metas selects either rows at or past the scan cursor or a bounded ID set.
// agent/model (added in 1.14.34) and cost/tokens (added in 1.14.49) are
// optional expressions because older OpenCode 1.x schemas lack them.
func (p *Provider) v1Metas(ctx context.Context, db v2Query, project bool, columns map[string]bool, sinceMs int64, full bool, selectedIDs []string, d *provider.Diagnostics) ([]model.SessionMeta, error) {
	worktree, join := "NULL", ""
	if project {
		worktree = "p.worktree"
		join = " LEFT JOIN project AS p ON p.id = s.project_id"
	}
	optional := func(name string) string {
		if columns[name] {
			return "s." + name
		}
		return "NULL"
	}
	query := `SELECT s.id, s.project_id, s.parent_id, s.directory, s.title, s.version,
		` + optional("agent") + `, ` + optional("model") + `, ` + optional("cost") + `,
		` + optional("tokens_input") + `, ` + optional("tokens_output") + `, ` + optional("tokens_reasoning") + `,
		` + optional("tokens_cache_read") + `, ` + optional("tokens_cache_write") + `,
		s.time_created, s.time_updated, s.time_archived, ` + worktree + `
		FROM session AS s` + join
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
		return nil, fmt.Errorf("v1 session rows: %w", err)
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
		var projectID, parent, dir, title, version, agentName, rawModel, worktree sql.NullString
		var cost sql.NullFloat64
		var input, output, reasoning, cacheRead, cacheWrite, created, updated, archived sql.NullInt64
		if err := rows.Scan(&id, &projectID, &parent, &dir, &title, &version, &agentName, &rawModel, &cost,
			&input, &output, &reasoning, &cacheRead, &cacheWrite, &created, &updated, &archived, &worktree); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("v1 session row: %w", err)
		}
		modelName, malformed := parseModelName(rawModel)
		if malformed {
			d.ParseErrors++
			d.Warn(p.dbPath(), 0, "session %q has invalid model JSON", id)
		}
		metas = append(metas, model.SessionMeta{
			Ref: model.SessionRef{Agent: model.AgentOpenCode, ID: id}, ParentID: parent.String,
			SourcePath: p.dbPath(), CWD: dir.String, Title: model.OneLine(title.String),
			AgentVersion: version.String, AgentName: agentName.String, Model: modelName,
			CreatedAt: unixMilli(created), UpdatedAt: unixMilli(updated), CostUSD: cost.Float64,
			Archived: archived.Valid, Tokens: model.TokenUsage{
				Input: input.Int64, Output: output.Int64, Reasoning: reasoning.Int64,
				CacheRead: cacheRead.Int64, CacheWrite: cacheWrite.Int64,
			},
		})
		origins[id] = metaOrigin{projectID: projectID.String, worktree: worktree}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("v1 session rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("v1 session rows close: %w", err)
	}
	for start := 0; start < len(metas); start += v2BatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+v2BatchSize, len(metas))
		if err := p.v1Counts(ctx, db, metas[start:end], d); err != nil {
			return nil, err
		}
	}
	p.finishMetas(metas, origins, d)
	return metas, nil
}

func (p *Provider) v1Counts(ctx context.Context, db v2Query, metas []model.SessionMeta, d *provider.Diagnostics) error {
	args := make([]any, len(metas))
	byID := make(map[string]*model.SessionMeta, len(metas))
	for i := range metas {
		args[i] = metas[i].Ref.ID
		byID[metas[i].Ref.ID] = &metas[i]
	}
	in := placeholders(len(args))
	roleQuery := `SELECT m.session_id,
		SUM(CASE WHEN CASE WHEN json_valid(m.data) THEN json_extract(m.data,'$.role') END='user'
			AND EXISTS (SELECT 1 FROM part AS p WHERE p.message_id=m.id
				AND CASE WHEN json_valid(p.data) THEN json_extract(p.data,'$.type') IN ('text','file') ELSE 0 END
				AND CASE WHEN json_valid(p.data) THEN COALESCE(json_extract(p.data,'$.synthetic'),0)=0 ELSE 0 END) THEN 1 ELSE 0 END),
		SUM(CASE WHEN CASE WHEN json_valid(m.data) THEN json_extract(m.data,'$.role') END='assistant' THEN 1 ELSE 0 END)
		FROM message AS m WHERE m.session_id IN (` + in + `) GROUP BY m.session_id`
	rows, err := db.QueryContext(ctx, roleQuery, args...)
	if err != nil {
		return fmt.Errorf("v1 role counts: %w", err)
	}
	for rows.Next() {
		var id string
		var users, assistants sql.NullInt64
		if err := rows.Scan(&id, &users, &assistants); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 role counts: %w", err)
		}
		if meta := byID[id]; meta != nil {
			meta.Counts.User, meta.Counts.Assistant = int(users.Int64), int(assistants.Int64)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 role counts: %w", err)
	}

	toolQuery := `SELECT session_id, COUNT(*) FROM part WHERE session_id IN (` + in + `)
		AND CASE WHEN json_valid(data) THEN json_extract(data,'$.type')='tool' ELSE 0 END GROUP BY session_id`
	rows, err = db.QueryContext(ctx, toolQuery, args...)
	if err != nil {
		return fmt.Errorf("v1 tool counts: %w", err)
	}
	for rows.Next() {
		var id string
		var tools int
		if err := rows.Scan(&id, &tools); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 tool counts: %w", err)
		}
		if meta := byID[id]; meta != nil {
			meta.Counts.ToolCalls = tools
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 tool counts: %w", err)
	}

	invalidMessage := `SELECT session_id, id FROM message WHERE session_id IN (` + in + `) AND COALESCE(json_valid(data),0)=0`
	rows, err = db.QueryContext(ctx, invalidMessage, args...)
	if err != nil {
		return fmt.Errorf("v1 invalid message JSON: %w", err)
	}
	for rows.Next() {
		var sessionID, messageID string
		if err := rows.Scan(&sessionID, &messageID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 invalid message JSON: %w", err)
		}
		d.ParseErrors++
		d.Warn(p.dbPath(), 0, "invalid JSON in session %q message %q", sessionID, messageID)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 invalid message JSON: %w", err)
	}

	invalidPart := `SELECT session_id, id FROM part WHERE session_id IN (` + in + `) AND COALESCE(json_valid(data),0)=0`
	rows, err = db.QueryContext(ctx, invalidPart, args...)
	if err != nil {
		return fmt.Errorf("v1 invalid part JSON: %w", err)
	}
	for rows.Next() {
		var sessionID, partID string
		if err := rows.Scan(&sessionID, &partID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 invalid part JSON: %w", err)
		}
		d.ParseErrors++
		d.Warn(p.dbPath(), 0, "invalid JSON in session %q part %q", sessionID, partID)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 invalid part JSON: %w", err)
	}

	known := `'text','reasoning','file','tool','step-start','step-finish','snapshot','patch','agent','subtask','retry','compaction'`
	unknownQuery := `SELECT CASE WHEN json_valid(data) THEN json_extract(data,'$.type') END, COUNT(*)
		FROM part WHERE session_id IN (` + in + `) AND json_valid(data)
		AND CASE WHEN json_valid(data) THEN
			json_extract(data,'$.type') IS NULL OR json_extract(data,'$.type') NOT IN (` + known + `)
			ELSE 0 END GROUP BY 1`
	rows, err = db.QueryContext(ctx, unknownQuery, args...)
	if err != nil {
		return fmt.Errorf("v1 part types: %w", err)
	}
	for rows.Next() {
		var kind sql.NullString
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 part types: %w", err)
		}
		name := kind.String
		if !kind.Valid || name == "" {
			name = "<null>"
		}
		for range count {
			d.Unknown(name)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 part types: %w", err)
	}

	// Choose the earliest conversational user message first, then its first
	// non-synthetic text. An attachment-only first prompt therefore has no text
	// preview rather than borrowing text from a later turn. File URI data is
	// never selected or decoded by this query.
	promptQuery := `WITH conversational AS (
		SELECT m.session_id, m.id,
			ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.time_created, m.id) AS message_rn
		FROM message AS m
		WHERE m.session_id IN (` + in + `)
			AND CASE WHEN json_valid(m.data) THEN json_extract(m.data,'$.role')='user' ELSE 0 END
			AND EXISTS (SELECT 1 FROM part AS candidate WHERE candidate.message_id=m.id
				AND CASE WHEN json_valid(candidate.data) THEN json_extract(candidate.data,'$.type') IN ('text','file') ELSE 0 END
				AND CASE WHEN json_valid(candidate.data) THEN COALESCE(json_extract(candidate.data,'$.synthetic'),0)=0 ELSE 0 END)
	), prompt_parts AS (
		SELECT c.session_id, json_extract(p.data,'$.text') AS text,
			ROW_NUMBER() OVER (PARTITION BY c.session_id ORDER BY p.id) AS part_rn
		FROM conversational AS c JOIN part AS p ON p.message_id=c.id
		WHERE c.message_rn=1
			AND CASE WHEN json_valid(p.data) THEN json_extract(p.data,'$.type')='text' ELSE 0 END
			AND CASE WHEN json_valid(p.data) THEN COALESCE(json_extract(p.data,'$.synthetic'),0)=0 ELSE 0 END
	)
	SELECT session_id, text FROM prompt_parts WHERE part_rn=1`
	rows, err = db.QueryContext(ctx, promptQuery, args...)
	if err != nil {
		return fmt.Errorf("v1 first prompts: %w", err)
	}
	for rows.Next() {
		var id string
		var text sql.NullString
		if err := rows.Scan(&id, &text); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 first prompts: %w", err)
		}
		if meta := byID[id]; meta != nil {
			meta.FirstPrompt = model.TruncateRunes(model.OneLine(text.String), 300)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 first prompts: %w", err)
	}

	// Keep FirstPrompt as the opening preview, but name untitled sessions from
	// their latest user message with non-synthetic text. Attachment-only turns
	// are skipped so they do not blank the name.
	titleQuery := `WITH texts AS (
		SELECT m.session_id, json_extract(p.data,'$.text') AS text,
			ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.time_created DESC, m.id DESC, p.id) AS rn
		FROM message AS m JOIN part AS p ON p.message_id=m.id
		WHERE m.session_id IN (` + in + `)
			AND CASE WHEN json_valid(m.data) THEN json_extract(m.data,'$.role')='user' ELSE 0 END
			AND CASE WHEN json_valid(p.data) THEN json_extract(p.data,'$.type')='text' ELSE 0 END
			AND CASE WHEN json_valid(p.data) THEN COALESCE(json_extract(p.data,'$.synthetic'),0)=0 ELSE 0 END
			AND CASE WHEN json_valid(p.data) THEN trim(json_extract(p.data,'$.text'), ' '||char(9,10,13))!='' ELSE 0 END
	)
	SELECT session_id, text FROM texts WHERE rn=1`
	rows, err = db.QueryContext(ctx, titleQuery, args...)
	if err != nil {
		return fmt.Errorf("v1 latest user prompts: %w", err)
	}
	for rows.Next() {
		var id string
		var text sql.NullString
		if err := rows.Scan(&id, &text); err != nil {
			_ = rows.Close()
			return fmt.Errorf("v1 latest user prompts: %w", err)
		}
		if meta := byID[id]; meta != nil && meta.Title == "" {
			meta.Title = model.TruncateRunes(model.OneLine(text.String), 300)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("v1 latest user prompts: %w", err)
	}
	return nil
}

func mergeIDLists(lists ...[]string) []string {
	set := make(map[string]bool)
	for _, list := range lists {
		for _, id := range list {
			set[id] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func selectedBatches(ids []string, fn func([]string) error) error {
	for start := 0; start < len(ids); start += v2BatchSize {
		if err := fn(ids[start:min(start+v2BatchSize, len(ids))]); err != nil {
			return err
		}
	}
	return nil
}
