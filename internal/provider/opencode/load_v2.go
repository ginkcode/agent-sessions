package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

const outputBudget = 64 << 10

// Load chooses the same generation as Scan: v2 wins unless a v1 duplicate
// has a strictly newer update time.
func (p *Provider) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	if err := validateV2Ref(ctx, ref); err != nil {
		return nil, err
	}
	db, err := p.openV2(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil, loadUnsupported(ctx)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	v2Ready, err := p.v2Ready(ctx, db)
	if err != nil {
		return nil, err
	}
	v1Session, err := sqliteread.HasTable(ctx, db, "session")
	if err != nil {
		return nil, err
	}
	v1Message, err := sqliteread.HasTable(ctx, db, "message")
	if err != nil {
		return nil, err
	}
	v1Part, err := sqliteread.HasTable(ctx, db, "part")
	if err != nil {
		return nil, err
	}
	v1Ready := v1Session && v1Message && v1Part
	if !v2Ready && !v1Ready {
		return nil, loadUnsupported(ctx)
	}
	project, err := sqliteread.HasTable(ctx, db, "project")
	if err != nil {
		return nil, err
	}
	var v1Columns map[string]bool
	if v1Ready {
		v1Columns, err = sqliteread.Columns(ctx, db, "session")
		if err != nil {
			return nil, err
		}
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("opencode begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	v2Updated, inV2, err := sessionUpdated(ctx, tx, "session_v2", ref.ID, v2Ready)
	if err != nil {
		return nil, err
	}
	v1Updated, inV1, err := sessionUpdated(ctx, tx, "session", ref.ID, v1Ready)
	if err != nil {
		return nil, err
	}
	generation := pickGeneration(inV2, v2Updated, inV1, v1Updated)
	if generation == "" {
		return nil, fmt.Errorf("opencode session %q not found", ref.ID)
	}

	diag := &provider.Diagnostics{}
	var metas []model.SessionMeta
	if generation == GenV2 {
		metas, err = p.v2Metas(ctx, tx, project, 0, false, []string{ref.ID}, diag)
	} else {
		metas, err = p.v1Metas(ctx, tx, project, v1Columns, 0, false, []string{ref.ID}, diag)
	}
	if err != nil {
		return nil, err
	}
	if len(metas) != 1 {
		return nil, fmt.Errorf("opencode session %q not found", ref.ID)
	}
	var transcript *model.Transcript
	if generation == GenV2 {
		transcript, err = p.loadV2(ctx, tx, ref.ID, metas[0], diag)
	} else {
		transcript, err = p.loadV1(ctx, tx, ref.ID, metas[0], diag)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("opencode commit read: %w", err)
	}
	return transcript, nil
}

func validateV2Ref(ctx context.Context, ref model.SessionRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Agent != model.AgentOpenCode || strings.TrimSpace(ref.ID) == "" || ref.ID != strings.TrimSpace(ref.ID) {
		return errors.New("invalid opencode session reference")
	}
	return nil
}

func (p *Provider) openV2(ctx context.Context) (*sql.DB, error) {
	path := p.dbPath()
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("opencode database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("opencode database is not a regular file")
	}
	return sqliteread.Open(ctx, path)
}

func (p *Provider) v2Ready(ctx context.Context, db *sql.DB) (bool, error) {
	session, err := sqliteread.HasTable(ctx, db, "session_v2")
	if err != nil {
		return false, err
	}
	message, err := sqliteread.HasTable(ctx, db, "session_message")
	if err != nil {
		return false, err
	}
	return session && message, nil
}

func sessionUpdated(ctx context.Context, db v2Query, table, id string, ready bool) (int64, bool, error) {
	if !ready {
		return 0, false, nil
	}
	var updated sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT time_updated FROM `+table+` WHERE id=?`, id).Scan(&updated)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("opencode %s session lookup: %w", table, err)
	}
	return updated.Int64, true, nil
}

func pickGeneration(inV2 bool, v2Updated int64, inV1 bool, v1Updated int64) string {
	if inV2 && (!inV1 || v2Updated >= v1Updated) {
		return GenV2
	}
	if inV1 {
		return GenV1
	}
	return ""
}

type v2Time struct {
	Created *int64 `json:"created"`
}

type v2User struct {
	Text  string `json:"text"`
	Time  v2Time `json:"time"`
	Files []struct {
		Name     string `json:"name"`
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		Size     int64  `json:"size"`
	} `json:"files"` // data is intentionally never decoded into a Go value.
}

type v2Assistant struct {
	Model   json.RawMessage   `json:"model"`
	Tokens  json.RawMessage   `json:"tokens"`
	Time    v2Time            `json:"time"`
	Content []json.RawMessage `json:"content"`
	Error   json.RawMessage   `json:"error"`
}

type v2Content struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Call  string          `json:"call"`
	State json.RawMessage `json:"state"`
}

type v2ToolState struct {
	Status   string          `json:"status"`
	Input    json.RawMessage `json:"input"`
	Metadata struct {
		Output    *string `json:"output"`
		Truncated bool    `json:"truncated"`
		SessionID string  `json:"sessionId"`
	} `json:"metadata"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// loadV2 streams ordered rows from the v2 store. The transaction supplied by
// Load fixes metadata, messages and child checks to one read-only snapshot.
func (p *Provider) loadV2(ctx context.Context, tx *sql.Tx, id string, meta model.SessionMeta, d *provider.Diagnostics) (*model.Transcript, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, type, seq, time_created, data FROM session_message WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("opencode v2 messages: %w", err)
	}
	transcript := &model.Transcript{Meta: meta, Messages: []model.Message{}}
	children := make(map[string][]*model.ToolCall)
	count := 0
	for rows.Next() {
		if count%1000 == 0 {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		count++
		var messageID string
		var kind sql.NullString
		var seq int64
		var created sql.NullInt64
		var data []byte
		if err := rows.Scan(&messageID, &kind, &seq, &created, &data); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("opencode v2 message row: %w", err)
		}
		if kind.String == "idle" || kind.String == "location-switched" {
			continue
		}
		if kind.String != "user" && kind.String != "assistant" && kind.String != "compaction" && kind.String != "system" && kind.String != "synthetic" {
			d.Unknown(safeKind(kind.String))
			continue
		}
		m := model.Message{ID: messageID, Time: unixMilli(created), Parts: []model.Part{}}
		if !json.Valid(data) || string(data) == "null" {
			warnV2JSON(d, p.dbPath(), messageID)
			continue
		}
		switch kind.String {
		case "user":
			var user v2User
			if err := json.Unmarshal(data, &user); err != nil {
				warnV2JSON(d, p.dbPath(), messageID)
				continue
			}
			m.Role = model.RoleUser
			m.Time = v2MessageTime(user.Time, created)
			if user.Text != "" {
				m.Parts = append(m.Parts, model.Part{Kind: model.PartText, Text: user.Text})
			}
			for i, file := range user.Files {
				name := file.Name
				if name == "" {
					name = file.Filename
				}
				m.Parts = append(m.Parts, model.Part{Kind: model.PartFile, File: &model.FileRef{
					Name: name, Mime: file.Mime, Size: file.Size, Ref: fmt.Sprintf("v2:%s:file:%d", messageID, i),
				}})
			}
		case "assistant":
			var assistant v2Assistant
			if err := json.Unmarshal(data, &assistant); err != nil {
				warnV2JSON(d, p.dbPath(), messageID)
				continue
			}
			m.Role = model.RoleAssistant
			m.Time = v2MessageTime(assistant.Time, created)
			m.Model = v2Model(assistant.Model)
			m.Tokens = v2Tokens(assistant.Tokens)
			for i, raw := range assistant.Content {
				var item v2Content
				if err := json.Unmarshal(raw, &item); err != nil || string(raw) == "null" {
					warnV2JSON(d, p.dbPath(), messageID)
					continue
				}
				switch item.Type {
				case "text":
					m.Parts = append(m.Parts, model.Part{Kind: model.PartText, Text: item.Text})
				case "reasoning":
					m.Parts = append(m.Parts, model.Part{Kind: model.PartReasoning, Text: item.Text})
				case "tool":
					var state v2ToolState
					if len(item.State) != 0 && json.Unmarshal(item.State, &state) != nil {
						warnV2JSON(d, p.dbPath(), messageID)
						continue
					}
					name := item.Name
					if name == "" {
						name = item.Call
					}
					tool := &model.ToolCall{ID: item.ID, Name: name, Input: state.Input, Status: v2ToolStatus(state.Status)}
					output, available := v2Output(state)
					if available {
						tool.Output, tool.OutputTruncated = outputPreview(output)
					}
					if tool.OutputTruncated || state.Metadata.Truncated {
						tool.OutputTruncated = true
						tool.OutputRef = fmt.Sprintf("v2:%s:tool:%d", messageID, i)
					}
					if name == "task" && state.Metadata.SessionID != "" {
						children[state.Metadata.SessionID] = append(children[state.Metadata.SessionID], tool)
					}
					m.Parts = append(m.Parts, model.Part{Kind: model.PartTool, Tool: tool})
				default:
					d.Unknown(safeKind(item.Type))
				}
			}
			if len(assistant.Error) != 0 && string(assistant.Error) != "null" {
				m.Parts = append(m.Parts, model.Part{Kind: model.PartNotice, Text: "Assistant error"})
			}
		case "compaction":
			var compact struct {
				Reason  string `json:"reason"`
				Status  string `json:"status"`
				Summary string `json:"summary"`
				Time    v2Time `json:"time"`
			}
			if err := json.Unmarshal(data, &compact); err != nil {
				warnV2JSON(d, p.dbPath(), messageID)
				continue
			}
			m.Role, m.IsMeta = model.RoleSystem, true
			m.Time = v2MessageTime(compact.Time, created)
			text := "Compaction"
			if compact.Reason != "" {
				text += ": " + compact.Reason
			}
			if compact.Status != "" {
				text += " (" + compact.Status + ")"
			}
			if compact.Summary != "" {
				preview, truncated := outputPreview(compact.Summary)
				text += "\n" + preview
				if truncated {
					m.Parts = append(m.Parts, model.Part{Kind: model.PartFile, File: &model.FileRef{Name: "Compaction summary", Mime: "text/plain", Ref: fmt.Sprintf("v2:%s:compaction:0", messageID)}})
				}
			}
			m.Parts = append([]model.Part{{Kind: model.PartCompaction, Text: text}}, m.Parts...)
		case "system", "synthetic":
			var notice struct {
				Text string `json:"text"`
				Time v2Time `json:"time"`
			}
			if err := json.Unmarshal(data, &notice); err != nil {
				warnV2JSON(d, p.dbPath(), messageID)
				continue
			}
			m.Role, m.IsMeta = model.RoleSystem, true
			m.Time = v2MessageTime(notice.Time, created)
			m.Parts = append(m.Parts, model.Part{Kind: model.PartNotice, Text: notice.Text})
		}
		transcript.Messages = append(transcript.Messages, m)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("opencode v2 messages: %w", err)
	}
	// Query child ownership only after closing the streaming rows: the SQLite
	// helper has a single connection. Never attach a child from an unverified ID.
	var tables []string
	if len(children) != 0 {
		var err error
		if tables, err = childTables(ctx, tx); err != nil {
			return nil, err
		}
	}
	for child, tools := range children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		verified, err := childHasParent(ctx, tx, tables, child, id)
		if err != nil {
			return nil, err
		}
		if verified {
			for _, tool := range tools {
				tool.Child = &model.SessionRef{Agent: model.AgentOpenCode, ID: child}
			}
		}
	}
	return transcript, nil
}

func safeKind(kind string) string {
	if kind == "" {
		return "<empty>"
	}
	if len(kind) > 64 {
		return "<invalid>"
	}
	for _, c := range kind {
		if c < 'a' || c > 'z' {
			if c < 'A' || c > 'Z' {
				if c < '0' || c > '9' {
					if c != '-' && c != '_' {
						return "<invalid>"
					}
				}
			}
		}
	}
	return kind
}

func warnV2JSON(d *provider.Diagnostics, path, id string) {
	d.ParseErrors++
	// Never include a parser error or raw JSON: either may contain file data.
	d.Warn(path, 0, "invalid v2 message JSON (id %q)", id)
}

func v2MessageTime(t v2Time, fallback sql.NullInt64) time.Time {
	if t.Created != nil {
		return time.UnixMilli(*t.Created).UTC()
	}
	return unixMilli(fallback)
}

func v2Model(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	name, _ := parseModelName(sql.NullString{String: string(raw), Valid: true})
	return name
}

func v2Tokens(raw json.RawMessage) model.TokenUsage {
	var fields struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		Reasoning  int64 `json:"reasoning"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
		Cache      struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return model.TokenUsage{}
	}
	if fields.CacheRead == 0 {
		fields.CacheRead = fields.Cache.Read
	}
	if fields.CacheWrite == 0 {
		fields.CacheWrite = fields.Cache.Write
	}
	return model.TokenUsage{Input: fields.Input, Output: fields.Output, Reasoning: fields.Reasoning, CacheRead: fields.CacheRead, CacheWrite: fields.CacheWrite}
}

func v2ToolStatus(status string) model.ToolStatus {
	switch status {
	case "pending", "running":
		return model.ToolPending
	case "completed", "success":
		return model.ToolCompleted
	case "error", "failed":
		return model.ToolError
	default:
		return model.ToolUnknown
	}
}

func v2Output(state v2ToolState) (string, bool) {
	if state.Metadata.Output != nil {
		return *state.Metadata.Output, true
	}
	var blocks []string
	for _, item := range state.Content {
		if item.Type == "text" {
			blocks = append(blocks, item.Text)
		}
	}
	if len(blocks) == 0 {
		return "", false
	}
	return strings.Join(blocks, "\n"), true
}

func outputPreview(s string) (string, bool) {
	if len(s) <= outputBudget {
		return s, false
	}
	end := outputBudget
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}
