package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/jsonl"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// Blob returns a full tool output or inline image/file bytes from a single
// record. Keys are rec:<lineStart>:<callID> or rec:<lineStart>:<content-index>.
// Arbitrary paths, external URLs and file IDs are never opened or fetched.
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	if !strings.HasPrefix(key, "rec:") {
		return nil, fmt.Errorf("codex: Blob: unsupported key %q", key)
	}
	offsetText, id, ok := strings.Cut(strings.TrimPrefix(key, "rec:"), ":")
	if !ok || id == "" || offsetText == "" || strings.Trim(offsetText, "0123456789") != "" {
		return nil, fmt.Errorf("codex: Blob: malformed key %q", key)
	}
	offset, err := strconv.ParseInt(offsetText, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("codex: Blob: malformed key %q: %w", key, err)
	}
	meta, err := p.rolloutFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := jsonl.Open(meta.SourcePath, offset)
	if err != nil {
		return nil, fmt.Errorf("codex: Blob %q: %w", key, err)
	}
	defer func() { _ = r.Close() }()
	line, _, err := r.Next()
	if errors.Is(err, io.EOF) && json.Valid(r.Trailing()) {
		line, err = r.Trailing(), nil // valid partial last line shown by Load
	}
	if err != nil {
		return nil, fmt.Errorf("codex: Blob %q: read record: %w", key, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var rec rolloutRecord
	if err := json.Unmarshal(line, &rec); err != nil || rec.Type != "response_item" {
		return nil, fmt.Errorf("codex: Blob %q: not a response item record", key)
	}
	var item responseItem
	if err := json.Unmarshal(rec.Payload, &item); err != nil {
		return nil, fmt.Errorf("codex: Blob %q: invalid response item: %w", key, err)
	}
	if item.Type == "message" {
		idx, err := strconv.Atoi(id)
		if err != nil || idx < 0 {
			return nil, fmt.Errorf("codex: Blob %q: invalid content index", key)
		}
		var blocks []json.RawMessage
		if err := json.Unmarshal(item.Content, &blocks); err != nil || idx >= len(blocks) {
			return nil, fmt.Errorf("codex: Blob %q: content index out of range", key)
		}
		var block contentPart
		if err := json.Unmarshal(blocks[idx], &block); err != nil {
			return nil, fmt.Errorf("codex: Blob %q: invalid content block: %w", key, err)
		}
		switch block.Type {
		case "input_image", "output_image", "image":
			return inlineData(block.ImageURL, key)
		case "input_file", "output_file", "file":
			return inlineData(block.FileData, key)
		default:
			return nil, fmt.Errorf("codex: Blob %q: content is not a file or image", key)
		}
	}
	switch item.Type {
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "web_search_call_output":
		callID := item.CallID
		if callID == "" {
			callID = item.ID
		}
		if callID != id {
			return nil, fmt.Errorf("codex: Blob %q: call ID mismatch", key)
		}
		return []byte(outputText(item.Output)), nil
	default:
		return nil, fmt.Errorf("codex: Blob %q: not a tool output record", key)
	}
}

// inlineData recognizes only inline base64/data URLs. A remote URL or local
// path cannot be used to make Blob read outside the validated rollout source.
func inlineData(raw json.RawMessage, key string) ([]byte, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return nil, fmt.Errorf("codex: Blob %q: no inline data", key)
	}
	if strings.HasPrefix(value, "data:") {
		prefix, encoded, ok := strings.Cut(value, ",")
		if !ok || !strings.HasSuffix(prefix, ";base64") {
			return nil, fmt.Errorf("codex: Blob %q: unsupported data URL", key)
		}
		value = encoded
	} else if strings.ContainsAny(value, "/?:") {
		return nil, fmt.Errorf("codex: Blob %q: external file or URL is not available", key)
	}
	bytes, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("codex: Blob %q: decode inline data: %w", key, err)
	}
	return bytes, nil
}
