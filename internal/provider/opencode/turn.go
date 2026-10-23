package opencode

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// lastMessageTurn is where a turn stands after a session's last message:
// a user prompt starts one; an assistant message with a tool pending or
// running waits on it (OpenCode runs a tool while it asks for permission);
// one that failed, or finished for any reason but more tool calls, ends it.
// The v2 store also records the session going idle.
func lastMessageTurn(kind, finish string, failed, toolRunning bool) string {
	switch kind {
	case "user":
		return model.TurnWorking
	case "idle":
		return model.TurnDone
	case "assistant":
		switch {
		case toolRunning:
			return model.TurnTool
		case failed:
			return model.TurnDone
		case finish != "" && finish != "tool-calls" && finish != "unknown":
			return model.TurnDone
		}
		return model.TurnWorking
	}
	return ""
}

// v1Turns sets each session's Turn from its last user or assistant message
// and that message's tool parts.
func v1Turns(ctx context.Context, db v2Query, in string, args []any, byID map[string]*model.SessionMeta) error {
	query := `WITH ranked AS (
		SELECT m.session_id, m.id, m.data,
			ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.time_created DESC, m.id DESC) AS rn
		FROM message AS m
		WHERE m.session_id IN (` + in + `)
			AND CASE WHEN json_valid(m.data) THEN json_extract(m.data,'$.role') IN ('user','assistant') ELSE 0 END
	)
	SELECT r.session_id, json_extract(r.data,'$.role'),
		CASE WHEN json_type(r.data,'$.finish')='text' THEN json_extract(r.data,'$.finish') ELSE '' END,
		COALESCE(json_type(r.data,'$.error'),'null')!='null',
		EXISTS (SELECT 1 FROM part AS p WHERE p.message_id=r.id
			AND CASE WHEN json_valid(p.data) THEN json_extract(p.data,'$.type')='tool'
				AND json_extract(p.data,'$.state.status') IN ('pending','running') ELSE 0 END)
	FROM ranked AS r WHERE r.rn=1`
	return scanTurns(ctx, db, query, args, byID, "v1")
}

// v2Turns does what v1Turns does for the v2 store, where tool calls are
// items of the assistant message's content.
func v2Turns(ctx context.Context, db v2Query, in string, args []any, byID map[string]*model.SessionMeta) error {
	query := `SELECT session_id, type, finish, failed, running FROM (
		SELECT m.session_id, m.type,
			CASE WHEN json_valid(m.data) AND json_type(m.data,'$.finish')='text' THEN json_extract(m.data,'$.finish') ELSE '' END AS finish,
			CASE WHEN json_valid(m.data) THEN COALESCE(json_type(m.data,'$.error'),'null')!='null' ELSE 0 END AS failed,
			CASE WHEN m.type='assistant' THEN EXISTS (SELECT 1 FROM
				json_each(CASE WHEN json_valid(m.data) THEN m.data ELSE '{}' END, '$.content') AS item
				WHERE CASE WHEN item.type='object' AND json_valid(item.value)
					THEN json_extract(item.value,'$.type')='tool'
						AND json_extract(item.value,'$.state.status') IN ('pending','running') ELSE 0 END) ELSE 0 END AS running,
			ROW_NUMBER() OVER (PARTITION BY m.session_id ORDER BY m.seq DESC, m.id DESC) AS rn
		FROM session_message AS m
		WHERE m.session_id IN (` + in + `) AND m.type IN ('user','assistant','idle')
	) WHERE rn=1`
	return scanTurns(ctx, db, query, args, byID, "v2")
}

func scanTurns(ctx context.Context, db v2Query, query string, args []any, byID map[string]*model.SessionMeta, gen string) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s turns: %w", gen, err)
	}
	for rows.Next() {
		var id string
		var kind, finish sql.NullString
		var failed, running bool
		if err := rows.Scan(&id, &kind, &finish, &failed, &running); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%s turns: %w", gen, err)
		}
		if meta := byID[id]; meta != nil {
			meta.Turn = lastMessageTurn(kind.String, finish.String, failed, running)
		}
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("%s turns: %w", gen, err)
	}
	return nil
}
