package opencode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// blobBudget is an explicit upper bound for bytes returned by Blob. The UI
// accepts a byte slice, not a stream; refuse oversized content rather than
// allocating unbounded decoded files or returning unexpectedly huge output.
const blobBudget = 16 << 20

// Blob retrieves one v1/v2 file or tool output on demand. Legacy JSON keys
// remain unsupported until their loader is implemented.
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	if err := validateV2Ref(ctx, ref); err != nil {
		return nil, err
	}
	generation, itemID, kind, index, err := parseBlobKey(key)
	if err != nil {
		return nil, err
	}
	db, err := p.openV2(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if generation == GenV1 {
		return p.blobV1(ctx, db, ref.ID, itemID, kind, index)
	}
	ready, err := p.v2Ready(ctx, db)
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, errors.New("opencode v2 tables are unavailable")
	}
	// The join verifies session ownership as well as the message ID: a key
	// from an unrelated session (or a stranded message) cannot retrieve data.
	var rowType sql.NullString
	var data []byte
	err = db.QueryRowContext(ctx, `SELECT m.type, m.data FROM session_message AS m
		JOIN session_v2 AS s ON s.id=m.session_id WHERE m.id=? AND m.session_id=?`, itemID, ref.ID).Scan(&rowType, &data)
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

func (p *Provider) blobV1(ctx context.Context, db *sql.DB, sessionID, partID, kind string, index int) ([]byte, error) {
	if index != 0 {
		return nil, errors.New("invalid opencode blob index")
	}
	session, err := sqliteread.HasTable(ctx, db, "session")
	if err != nil {
		return nil, err
	}
	message, err := sqliteread.HasTable(ctx, db, "message")
	if err != nil {
		return nil, err
	}
	part, err := sqliteread.HasTable(ctx, db, "part")
	if err != nil {
		return nil, err
	}
	if !session || !message || !part {
		return nil, errors.New("opencode v1 tables are unavailable")
	}
	var path, wantType string
	var limit int64
	switch kind {
	case "file":
		// Encoded data URIs expand by at most 3x (percent-encoding);
		// decodeDataURI enforces the decoded budget exactly.
		path, wantType, limit = "'$.url'", "file", 3*blobBudget+4096
	case "tool":
		path = `CASE WHEN json_extract(p.data,'$.state.status') IN ('error','failed')
			THEN '$.state.error' ELSE '$.state.output' END`
		wantType, limit = "tool", blobBudget
	default:
		return nil, errors.New("invalid opencode blob key")
	}
	// Extract and size only the requested field in SQL so neither the rest of
	// the part (attachments, tool input) nor an over-budget value is ever
	// materialized. Ownership matches loadV1: the part's message must belong
	// to the same session.
	from := ` FROM part AS p
		JOIN message AS m ON m.id=p.message_id AND m.session_id=p.session_id
		JOIN session AS s ON s.id=p.session_id
		WHERE p.id=? AND p.session_id=?`
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("opencode v1 blob lookup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var valid bool
	var partType, valueType sql.NullString
	var size sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT json_valid(p.data),
		CASE WHEN json_valid(p.data) THEN json_extract(p.data,'$.type') END,
		CASE WHEN json_valid(p.data) THEN json_type(p.data, `+path+`) END,
		CASE WHEN json_valid(p.data) THEN length(CAST(json_extract(p.data, `+path+`) AS BLOB)) END`+from,
		partID, sessionID).Scan(&valid, &partType, &valueType, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("opencode blob not found for session")
	}
	if err != nil {
		return nil, fmt.Errorf("opencode v1 blob lookup: %w", err)
	}
	if !valid {
		return nil, errors.New("opencode blob has invalid JSON")
	}
	if partType.String != wantType {
		return nil, fmt.Errorf("opencode blob is not a %s", wantType)
	}
	if valueType.String != "text" || size.Int64 == 0 && kind == "file" {
		if kind == "file" {
			return nil, errors.New("opencode blob is not a file")
		}
		return nil, errors.New("opencode tool output unavailable")
	}
	if size.Int64 > limit {
		if kind == "file" {
			return nil, errors.New("opencode file exceeds blob budget")
		}
		return nil, errors.New("opencode tool output exceeds blob budget")
	}
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT json_extract(p.data, `+path+`)`+from, partID, sessionID).Scan(&value); err != nil {
		return nil, fmt.Errorf("opencode v1 blob lookup: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if kind == "file" {
		return decodeDataURI(value)
	}
	return []byte(value), nil
}

func decodeDataURI(uri string) ([]byte, error) {
	if len(uri) < 5 || !strings.EqualFold(uri[:5], "data:") {
		return nil, errors.New("opencode file content is not stored inline")
	}
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, errors.New("opencode file has invalid data URI")
	}
	header, payload := uri[5:comma], uri[comma+1:]
	base64Encoded := false
	for _, parameter := range strings.Split(header, ";")[1:] {
		if strings.EqualFold(parameter, "base64") {
			base64Encoded = true
		}
	}
	if base64Encoded {
		if base64.StdEncoding.DecodedLen(len(payload)) > blobBudget+2 {
			return nil, errors.New("opencode file exceeds blob budget")
		}
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil && len(payload)%4 != 0 {
			decoded, err = base64.RawStdEncoding.DecodeString(payload)
		}
		if err != nil {
			return nil, errors.New("opencode file has invalid base64")
		}
		if len(decoded) > blobBudget {
			return nil, errors.New("opencode file exceeds blob budget")
		}
		return decoded, nil
	}
	if len(payload) > blobBudget*3 {
		return nil, errors.New("opencode file exceeds blob budget")
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, errors.New("opencode file has invalid data URI")
	}
	if len(decoded) > blobBudget {
		return nil, errors.New("opencode file exceeds blob budget")
	}
	return []byte(decoded), nil
}

func parseBlobKey(key string) (generation, itemID, kind string, index int, err error) {
	switch {
	case strings.HasPrefix(key, "v2:"):
		generation = GenV2
	case strings.HasPrefix(key, "v1:"):
		generation = GenV1
	default:
		return "", "", "", 0, blobUnsupported(context.Background())
	}
	// IDs are opaque, and may themselves contain colons. Parse delimiters from
	// the end rather than splitting the entire key into a fixed number of words.
	rest := strings.TrimPrefix(key, generation+":")
	last := strings.LastIndexByte(rest, ':')
	if last < 0 {
		return "", "", "", 0, errors.New("invalid opencode blob key")
	}
	position := rest[last+1:]
	prefix := rest[:last]
	middle := strings.LastIndexByte(prefix, ':')
	if middle < 1 || len(position) == 0 || len(position) > 9 || (len(position) > 1 && position[0] == '0') {
		return "", "", "", 0, errors.New("invalid opencode blob key")
	}
	itemID, kind = prefix[:middle], prefix[middle+1:]
	if kind != "file" && kind != "tool" && (generation != GenV2 || kind != "compaction") {
		return "", "", "", 0, errors.New("invalid opencode blob key")
	}
	parsed, parseErr := strconv.Atoi(position)
	if parseErr != nil || parsed < 0 || generation == GenV1 && parsed != 0 {
		return "", "", "", 0, errors.New("invalid opencode blob index")
	}
	return generation, itemID, kind, parsed, nil
}
