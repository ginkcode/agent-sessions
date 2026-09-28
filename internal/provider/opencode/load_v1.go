package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

type v1MessageData struct {
	Role       string          `json:"role"`
	Time       v2Time          `json:"time"`
	ModelID    string          `json:"modelID"`
	ProviderID string          `json:"providerID"`
	Tokens     json.RawMessage `json:"tokens"`
	Error      json.RawMessage `json:"error"`
}

type v1PartData struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Synthetic   bool            `json:"synthetic"`
	Mime        string          `json:"mime"`
	Filename    string          `json:"filename"`
	Tool        string          `json:"tool"`
	CallID      string          `json:"callID"`
	State       json.RawMessage `json:"state"`
	Name        string          `json:"name"`
	Agent       string          `json:"agent"`
	Description string          `json:"description"`
	Attempt     int             `json:"attempt"`
	Error       json.RawMessage `json:"error"`
	Auto        bool            `json:"auto"`
	Overflow    bool            `json:"overflow"`
}

type v1ToolState struct {
	Status   string          `json:"status"`
	Input    json.RawMessage `json:"input"`
	Output   *string         `json:"output"`
	Error    *string         `json:"error"`
	Metadata struct {
		SessionID       string `json:"sessionId"`
		ParentSessionID string `json:"parentSessionId"`
	} `json:"metadata"`
}

func (p *Provider) loadV1(ctx context.Context, tx *sql.Tx, id string, meta model.SessionMeta, d *provider.Diagnostics) (*model.Transcript, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, time_created, data FROM message
		WHERE session_id=? ORDER BY time_created, id`, id)
	if err != nil {
		return nil, fmt.Errorf("opencode v1 messages: %w", err)
	}
	transcript := &model.Transcript{Meta: meta, Messages: []model.Message{}}
	messageIndex := make(map[string]int)
	messageError := make(map[string]bool)
	for rows.Next() {
		if len(transcript.Messages)%1000 == 0 {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		var messageID string
		var created sql.NullInt64
		var data []byte
		if err := rows.Scan(&messageID, &created, &data); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("opencode v1 message row: %w", err)
		}
		var info v1MessageData
		if !json.Valid(data) || string(data) == "null" || json.Unmarshal(data, &info) != nil {
			warnV1JSON(d, p.dbPath(), "message", messageID)
			continue
		}
		message := model.Message{ID: messageID, Time: v2MessageTime(info.Time, created), Parts: []model.Part{}}
		switch info.Role {
		case "user":
			message.Role = model.RoleUser
		case "assistant":
			message.Role = model.RoleAssistant
			message.Model = joinModel(info.ProviderID, info.ModelID)
			message.Tokens = v2Tokens(info.Tokens)
			messageError[messageID] = len(info.Error) != 0 && string(info.Error) != "null"
		default:
			d.Unknown(safeKind(info.Role))
			continue
		}
		messageIndex[messageID] = len(transcript.Messages)
		transcript.Messages = append(transcript.Messages, message)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("opencode v1 messages: %w", err)
	}

	// File URLs and tool attachments are removed in SQL before JSON reaches Go.
	rows, err = tx.QueryContext(ctx, `SELECT p.message_id, p.id,
		CASE WHEN COALESCE(json_valid(p.data),0)=0 THEN p.data
			WHEN json_extract(p.data,'$.type')='file' THEN json_remove(p.data,'$.url')
			WHEN json_extract(p.data,'$.type')='tool' THEN json_remove(p.data,'$.state.attachments')
			ELSE p.data END
		FROM part AS p JOIN message AS m ON m.id=p.message_id
		WHERE p.session_id=? AND m.session_id=? ORDER BY p.message_id, p.id`, id, id)
	if err != nil {
		return nil, fmt.Errorf("opencode v1 parts: %w", err)
	}
	children := make(map[string][]*model.ToolCall)
	syntheticOnly := make(map[string]bool)
	for messageID := range messageIndex {
		if transcript.Messages[messageIndex[messageID]].Role == model.RoleUser {
			syntheticOnly[messageID] = true
		}
	}
	count := 0
	for rows.Next() {
		if count%1000 == 0 {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		count++
		var messageID, partID string
		var data []byte
		if err := rows.Scan(&messageID, &partID, &data); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("opencode v1 part row: %w", err)
		}
		index, ok := messageIndex[messageID]
		if !ok {
			continue
		}
		var part v1PartData
		if !json.Valid(data) || string(data) == "null" || json.Unmarshal(data, &part) != nil {
			warnV1JSON(d, p.dbPath(), "part", partID)
			continue
		}
		message := &transcript.Messages[index]
		switch part.Type {
		case "text":
			if part.Synthetic {
				message.Parts = append(message.Parts, model.Part{Kind: model.PartNotice, Text: part.Text})
			} else {
				message.Parts = append(message.Parts, model.Part{Kind: model.PartText, Text: part.Text})
				syntheticOnly[messageID] = false
			}
		case "reasoning":
			message.Parts = append(message.Parts, model.Part{Kind: model.PartReasoning, Text: part.Text})
		case "file":
			name := part.Filename
			if name == "" {
				name = "attachment"
			}
			message.Parts = append(message.Parts, model.Part{Kind: model.PartFile, File: &model.FileRef{
				Name: name, Mime: part.Mime, Ref: fmt.Sprintf("v1:%s:file:0", partID),
			}})
			syntheticOnly[messageID] = false
		case "tool":
			var state v1ToolState
			if len(part.State) != 0 && json.Unmarshal(part.State, &state) != nil {
				warnV1JSON(d, p.dbPath(), "part", partID)
				continue
			}
			name := part.Tool
			if name == "" {
				name = part.CallID
			}
			tool := &model.ToolCall{ID: part.CallID, Name: name, Input: state.Input, Status: v2ToolStatus(state.Status)}
			var output *string
			if state.Status == "error" || state.Status == "failed" {
				output = state.Error
			} else {
				output = state.Output
			}
			if output != nil {
				tool.Output, tool.OutputTruncated = outputPreview(*output)
				if tool.OutputTruncated {
					tool.OutputRef = fmt.Sprintf("v1:%s:tool:0", partID)
				}
			}
			if name == "task" && state.Metadata.SessionID != "" &&
				(state.Metadata.ParentSessionID == "" || state.Metadata.ParentSessionID == id) {
				children[state.Metadata.SessionID] = append(children[state.Metadata.SessionID], tool)
			}
			message.Parts = append(message.Parts, model.Part{Kind: model.PartTool, Tool: tool})
		case "compaction":
			text := "Compaction"
			if part.Auto {
				text += " (auto)"
			}
			if part.Overflow {
				text += " (overflow)"
			}
			message.Parts = append(message.Parts, model.Part{Kind: model.PartCompaction, Text: text})
		case "agent":
			message.Parts = append(message.Parts, model.Part{Kind: model.PartNotice, Text: metaLine("Agent", part.Name)})
		case "subtask":
			label := "Subtask"
			if part.Agent != "" {
				label += " (@" + part.Agent + ")"
			}
			message.Parts = append(message.Parts, model.Part{Kind: model.PartNotice, Text: metaLine(label, part.Description)})
		case "retry":
			message.Parts = append(message.Parts, model.Part{Kind: model.PartNotice, Text: retryLine(part)})
		case "step-start", "step-finish", "snapshot", "patch":
			// Bookkeeping-only parts are intentionally omitted from transcripts.
		default:
			d.Unknown(safeKind(part.Type))
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("opencode v1 parts: %w", err)
	}

	for i := range transcript.Messages {
		message := &transcript.Messages[i]
		if syntheticOnly[message.ID] {
			message.Role, message.IsMeta = model.RoleSystem, true
		}
		if messageError[message.ID] {
			message.Parts = append(message.Parts, model.Part{Kind: model.PartNotice, Text: "Assistant error"})
		}
	}
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

// childTables probes once per load which session tables can confirm a task
// child's parent link, so childHasParent does not re-probe per child.
func childTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	var tables []string
	for _, table := range []string{"session_v2", "session"} {
		var present bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&present); err != nil {
			return nil, fmt.Errorf("opencode child schema lookup: %w", err)
		}
		if present {
			tables = append(tables, table)
		}
	}
	return tables, nil
}

func childHasParent(ctx context.Context, tx *sql.Tx, tables []string, child, parent string) (bool, error) {
	for _, table := range tables {
		var value sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT parent_id FROM `+table+` WHERE id=?`, child).Scan(&value)
		if err == nil && value.String == parent {
			return true, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("opencode %s child lookup: %w", table, err)
		}
	}
	return false, nil
}

func joinModel(providerID, modelID string) string {
	if modelID == "" {
		return providerID
	}
	if providerID == "" {
		return modelID
	}
	return providerID + "/" + modelID
}

func warnV1JSON(d *provider.Diagnostics, path, kind, id string) {
	d.ParseErrors++
	d.Warn(path, 0, "invalid v1 %s JSON (id %q)", kind, id)
}

func metaLine(label, value string) string {
	value = model.TruncateRunes(model.OneLine(value), 300)
	if value == "" {
		return label
	}
	return label + ": " + value
}

func retryLine(part v1PartData) string {
	label := fmt.Sprintf("Retry %d", part.Attempt)
	if part.Attempt == 0 {
		label = "Retry"
	}
	var detail struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(part.Error, &detail) == nil && detail.Name != "" {
		return label + ": " + safeKind(detail.Name)
	}
	return label
}
