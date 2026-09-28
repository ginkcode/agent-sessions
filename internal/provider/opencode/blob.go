package opencode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// blobBudget is an explicit upper bound for bytes returned by Blob. The UI
// accepts a byte slice, not a stream; refuse oversized content rather than
// allocating unbounded decoded files or returning unexpectedly huge output.
const blobBudget = 16 << 20

// Blob retrieves one v2 file, tool output or compaction summary on demand.
// v1/legacy blob keys remain unsupported until their respective loaders land.
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	if err := validateV2Ref(ctx, ref); err != nil {
		return nil, err
	}
	messageID, kind, index, err := parseV2BlobKey(key)
	if err != nil {
		return nil, err
	}
	db, err := p.openV2(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	ready, err := p.v2Ready(ctx, db)
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, blobUnsupported(ctx)
	}
	// The join verifies session ownership as well as the message ID: a key
	// from an unrelated session (or a stranded message) cannot retrieve data.
	var rowType sql.NullString
	var data []byte
	err = db.QueryRowContext(ctx, `SELECT m.type, m.data FROM session_message AS m
		JOIN session_v2 AS s ON s.id=m.session_id WHERE m.id=? AND m.session_id=?`, messageID, ref.ID).Scan(&rowType, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("opencode blob not found for session")
	}
	if err != nil {
		return nil, fmt.Errorf("opencode v2 blob lookup: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("opencode blob has invalid JSON")
	}
	switch kind {
	case "file":
		if rowType.String != "user" {
			return nil, errors.New("opencode blob is not a user file")
		}
		var obj struct {
			Files []json.RawMessage `json:"files"`
		}
		if json.Unmarshal(data, &obj) != nil || index >= len(obj.Files) {
			return nil, errors.New("opencode file index or JSON invalid")
		}
		var file struct {
			Data string `json:"data"`
		}
		if json.Unmarshal(obj.Files[index], &file) != nil || file.Data == "" {
			return nil, errors.New("opencode file data unavailable")
		}
		if base64.StdEncoding.DecodedLen(len(file.Data)) > blobBudget+2 {
			return nil, errors.New("opencode file exceeds blob budget")
		}
		decoded, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			return nil, errors.New("opencode file has invalid base64")
		}
		if len(decoded) > blobBudget {
			return nil, errors.New("opencode file exceeds blob budget")
		}
		return decoded, nil
	case "tool":
		if rowType.String != "assistant" {
			return nil, errors.New("opencode blob is not assistant tool output")
		}
		var obj struct {
			Content []json.RawMessage `json:"content"`
		}
		if json.Unmarshal(data, &obj) != nil || index >= len(obj.Content) {
			return nil, errors.New("opencode tool index or JSON invalid")
		}
		var item v2Content
		if json.Unmarshal(obj.Content[index], &item) != nil || item.Type != "tool" {
			return nil, errors.New("opencode blob is not a tool")
		}
		var state v2ToolState
		if json.Unmarshal(item.State, &state) != nil {
			return nil, errors.New("opencode tool state invalid")
		}
		output, ok := v2Output(state)
		if !ok {
			return nil, errors.New("opencode tool output unavailable")
		}
		if len(output) > blobBudget {
			return nil, errors.New("opencode tool output exceeds blob budget")
		}
		return []byte(output), nil
	case "compaction":
		if rowType.String != "compaction" || index != 0 {
			return nil, errors.New("opencode blob is not a compaction summary")
		}
		var obj struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal(data, &obj) != nil {
			return nil, errors.New("opencode compaction summary invalid")
		}
		if len(obj.Summary) > blobBudget {
			return nil, errors.New("opencode compaction summary exceeds blob budget")
		}
		return []byte(obj.Summary), nil
	default:
		return nil, errors.New("invalid opencode blob key")
	}
}

func parseV2BlobKey(key string) (messageID, kind string, index int, err error) {
	if !strings.HasPrefix(key, "v2:") {
		// v1 and legacy blob keys belong to their own loaders (M1-05/M1-06).
		return "", "", 0, blobUnsupported(context.Background())
	}
	// IDs are opaque, and may themselves contain colons. Parse delimiters from
	// the end rather than splitting the entire key into a fixed number of words.
	rest := strings.TrimPrefix(key, "v2:")
	last := strings.LastIndexByte(rest, ':')
	if last < 0 {
		return "", "", 0, errors.New("invalid opencode blob key")
	}
	position := rest[last+1:]
	prefix := rest[:last]
	middle := strings.LastIndexByte(prefix, ':')
	if middle < 1 || len(position) == 0 || len(position) > 9 || (len(position) > 1 && position[0] == '0') {
		return "", "", 0, errors.New("invalid opencode blob key")
	}
	messageID, kind = prefix[:middle], prefix[middle+1:]
	if kind != "file" && kind != "tool" && kind != "compaction" {
		return "", "", 0, errors.New("invalid opencode blob key")
	}
	parsed, parseErr := strconv.Atoi(position)
	if parseErr != nil || parsed < 0 {
		return "", "", 0, errors.New("invalid opencode blob index")
	}
	return messageID, kind, parsed, nil
}
