package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/jsonl"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

const (
	// maxOutputBytes is the largest tool output inlined into a transcript.
	// Larger outputs are truncated; Blob returns the full text.
	maxOutputBytes = 64 << 10
	// maxAttachmentRunes caps the rendered text of attachment records.
	maxAttachmentRunes = 4096
	// toolResultsDir holds large tool outputs next to the session file.
	toolResultsDir = "tool-results"
	// toolResultsMarker appears in tool output text that references a
	// file in the tool-results directory; Windows writes backslashes.
	toolResultsMarker        = "/" + toolResultsDir + "/"
	toolResultsMarkerWindows = `\` + toolResultsDir + `\`
)

// rawRecord is the narrow decoding of one transcript line.
type rawRecord struct {
	Type             string          `json:"type"`
	UUID             string          `json:"uuid"`
	Timestamp        json.RawMessage `json:"timestamp"`
	IsMeta           bool            `json:"isMeta"`
	IsSidechain      bool            `json:"isSidechain"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Subtype          string          `json:"subtype"`
	Content          string          `json:"content"` // system records
	CompactMetadata  *compactMeta    `json:"compactMetadata"`
	Message          *rawMessage     `json:"message"`
	Attachment       *attachmentRec  `json:"attachment"`
	ToolUseResult    json.RawMessage `json:"toolUseResult"` // not decoded in M0
	IsAPIError       bool            `json:"isApiErrorMessage"`
}

type compactMeta struct {
	Trigger    string `json:"trigger"`
	PreTokens  int64  `json:"preTokens"`
	PostTokens int64  `json:"postTokens"`
}

type attachmentRec struct {
	Type     string `json:"type"`
	Rendered string `json:"rendered"`
}

type rawMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // string | []block
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens         int64                `json:"input_tokens"`
	OutputTokens        int64                `json:"output_tokens"`
	CacheReadTokens     int64                `json:"cache_read_input_tokens"`
	CacheCreationTokens int64                `json:"cache_creation_input_tokens"`
	OutputTokensDetails *outputTokensDetails `json:"output_tokens_details"`
}

type outputTokensDetails struct {
	ThinkingTokens int64 `json:"thinking_tokens"`
}

func (u *rawUsage) usage() model.TokenUsage {
	if u == nil {
		return model.TokenUsage{}
	}
	t := model.TokenUsage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadTokens,
		CacheWrite: u.CacheCreationTokens,
	}
	if u.OutputTokensDetails != nil {
		t.Reasoning = u.OutputTokensDetails.ThinkingTokens
	}
	return t
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // tool_result: string | []block
	IsError   bool            `json:"is_error"`
	Source    *imageSource    `json:"source"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
}

// imageData is decoded only on a Blob request, never while loading a
// transcript. Images can contain megabytes of base64 per record.
type imageData struct {
	Source struct {
		Data string `json:"data"`
	} `json:"source"`
}

// subMeta is the content of subagents/agent-X.meta.json.
type subMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
}

// Load returns the full transcript for a session reference.
func (p *Provider) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	s, err := p.sourceFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	t, _, err := p.loadFile(ctx, s)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// loadFile reads one transcript file into a Transcript. The Diagnostics it
// returns are only used by tests; Load itself discards them (scanning owns
// the reported diagnostics).
func (p *Provider) loadFile(ctx context.Context, s source) (*model.Transcript, *provider.Diagnostics, error) {
	st, err := os.Stat(s.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("claude: load %s: %w", s.Path, err)
	}

	ref := sessionRef(s)
	cp := &checkpoint{
		Meta: model.SessionMeta{
			Ref:        ref,
			SourcePath: s.Path,
			ParentID:   s.ParentID,
		},
		path: s.Path,
	}

	if s.MetaPath != "" {
		if sub, err := readSubMeta(s.MetaPath); err == nil {
			cp.Meta.AgentName = sub.AgentType
			cp.Meta.ParentToolCallID = sub.ToolUseID
			cp.SubagentDescription = sub.Description
		}
	}

	r, err := jsonl.Open(s.Path, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("claude: load %s: %w", s.Path, err)
	}
	defer func() { _ = r.Close() }()

	l := &loader{
		path:       s.Path,
		tools:      make(map[string]*model.ToolCall),
		compactMsg: -1,
		diag:       &provider.Diagnostics{},
	}
	// The checkpoint keeps its own diagnostics: unknown-type counting in
	// consume would otherwise double every entry the loader also records.
	scanDiag := &provider.Diagnostics{}
	for n := 0; ; n++ {
		if n%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, fmt.Errorf("claude: load %s: %w", s.Path, err)
			}
		}
		line, start, err := r.Next()
		if errors.Is(err, io.EOF) {
			// A live process may leave a valid JSON object without a newline.
			// Scan will wait for the newline before checkpointing it, while
			// Load can show it best-effort without changing any persistent state.
			if trailing := r.Trailing(); json.Valid(trailing) {
				cp.lineNo = r.LineNo() + 1
				cp.consume(trailing, l.diag)
				l.record(trailing, r.Offset(), cp.lineNo)
			}
			break
		}
		if errors.Is(err, jsonl.ErrLineTooLong) {
			l.diag.Warn(s.Path, r.LineNo(), "line too long, skipped")
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("claude: read %s: %w", s.Path, err)
		}
		cp.lineNo = r.LineNo()
		cp.consume(line, scanDiag)
		if json.Valid(line) {
			l.record(line, start, r.LineNo())
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("claude: load %s: %w", s.Path, err)
	}

	meta := cp.finalize(st.ModTime())
	meta.Ref = ref
	meta.SourcePath = s.Path
	meta.ParentID = s.ParentID

	liveID := meta.Ref.ID
	if s.ParentID != "" {
		liveID = s.ParentID
	}
	var liveStatus string
	live, err := p.Live(ctx)
	if err == nil {
		if info, ok := live[liveID]; ok {
			liveStatus = info.Status
			if s.ParentID == "" {
				meta.Live = true
				meta.LiveStatus = info.Status
			}
		}
	}
	// Unanswered tool calls: pending while the session is live, otherwise
	// the outcome is unknown.
	if liveStatus == "" {
		for _, tc := range l.tools {
			if tc.Status == model.ToolPending {
				tc.Status = model.ToolUnknown
			}
		}
	}

	if meta.CWD != "" {
		norm := pathutil.NormalizeDir(meta.CWD)
		meta.CWD = norm
		meta.CWDMissing = !pathutil.Exists(norm)
		if repo, ok := p.git.Resolve(norm); ok {
			meta.RepoRoot = repo.MainRoot
		}
	}

	if s.ParentID == "" {
		p.linkChildren(ctx, meta.Ref.ID, l.tools)
	}

	return &model.Transcript{Meta: meta, Messages: l.msgs}, l.diag, nil
}

// readSubMeta loads a subagent's small meta.json, best effort.
func readSubMeta(path string) (subMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return subMeta{}, err
	}
	var m subMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return subMeta{}, fmt.Errorf("claude: parse %s: %w", path, err)
	}
	return m, nil
}

// linkChildren sets the child session reference on tool calls that spawned
// subagents of the given parent session.
func (p *Provider) linkChildren(ctx context.Context, parentID string, tools map[string]*model.ToolCall) {
	sources, err := p.discover(ctx)
	if err != nil {
		return
	}
	for _, sub := range sources {
		if sub.ParentID != parentID {
			continue
		}
		m, err := readSubMeta(sub.MetaPath)
		if err != nil || m.ToolUseID == "" {
			continue
		}
		tc, ok := tools[m.ToolUseID]
		if !ok || tc.Child != nil {
			continue
		}
		tc.Child = &model.SessionRef{
			Agent: model.AgentClaude,
			ID:    sub.ParentID + "/agent-" + sub.AgentID,
		}
	}
}

// loader builds messages from a transcript file in a single pass.
type loader struct {
	path  string
	msgs  []model.Message
	curID string
	// curIdx is the index of the current (split) assistant message.
	curIdx      int
	hasCur      bool
	tools       map[string]*model.ToolCall
	compactMsg  int // index of the message holding the open compaction part
	compactPart int
	diag        *provider.Diagnostics
}

func (l *loader) record(line []byte, start int64, lineNo int) {
	var rec rawRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		l.diag.ParseErrors++
		l.diag.Warn(l.path, lineNo, "invalid JSON record: %v", err)
		return
	}
	switch rec.Type {
	case "assistant":
		l.assistant(&rec, lineNo)
	case "user":
		l.user(&rec, start, lineNo)
	case "system":
		l.system(&rec)
	case "attachment":
		l.attachment(&rec)
	case "ai-title", "summary", "last-prompt", "mode", "cost-state",
		"queue-operation", "atis-latch":
		// Bookkeeping handled by the scan aggregator.
	default:
		l.diag.Unknown(rec.Type)
	}
}

func (l *loader) assistant(rec *rawRecord, lineNo int) {
	m := rec.Message
	if m == nil {
		l.diag.Warn(l.path, lineNo, "assistant record without message")
		return
	}
	if !l.hasCur || l.curID != m.ID {
		l.msgs = append(l.msgs, model.Message{
			ID:          m.ID,
			Role:        model.RoleAssistant,
			Time:        parseTime(rec.Timestamp),
			Model:       m.Model,
			IsSidechain: rec.IsSidechain,
		})
		l.curIdx = len(l.msgs) - 1
		l.hasCur = true
		l.curID = m.ID
	}
	cur := &l.msgs[l.curIdx]
	if m.Model != "" {
		cur.Model = m.Model
	}
	cur.Tokens = m.Usage.usage()

	for _, b := range contentBlocks(m.Content) {
		switch b.Type {
		case "text":
			if b.Text != "" {
				cur.Parts = append(cur.Parts, model.Part{Kind: model.PartText, Text: b.Text})
			}
		case "thinking":
			if b.Thinking != "" {
				cur.Parts = append(cur.Parts, model.Part{Kind: model.PartReasoning, Text: b.Thinking})
			}
		case "tool_use":
			tc := &model.ToolCall{ID: b.ID, Name: b.Name, Input: b.Input, Status: model.ToolPending}
			cur.Parts = append(cur.Parts, model.Part{Kind: model.PartTool, Tool: tc})
			l.tools[b.ID] = tc
		}
	}
}

func (l *loader) user(rec *rawRecord, start int64, lineNo int) {
	m := rec.Message
	if m == nil {
		return
	}
	text, blocks := decodeContent(m.Content)

	if rec.IsCompactSummary {
		if text == "" {
			text = joinTextBlocks(blocks)
		}
		if text != "" && l.compactMsg >= 0 && l.compactMsg < len(l.msgs) {
			part := &l.msgs[l.compactMsg].Parts[l.compactPart]
			part.Text += "\n\n" + text
		}
		l.compactMsg, l.compactPart = -1, 0
		return
	}

	// Pair tool results with their calls first.
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		out := outputText(b.Content)
		out, ref, truncated := clipOutput(out, start, b.ToolUseID)
		if tc, ok := l.tools[b.ToolUseID]; ok {
			tc.Output = out
			tc.OutputRef = ref
			tc.OutputTruncated = truncated || tc.OutputTruncated
			if b.IsError {
				tc.Status = model.ToolError
			} else if tc.Status == model.ToolPending || tc.Status == model.ToolUnknown {
				tc.Status = model.ToolCompleted
			}
		} else {
			l.diag.Warn(l.path, lineNo, "tool result for unknown call %s", b.ToolUseID)
		}
	}

	metaText := rec.IsMeta
	var parts []model.Part
	if text != "" {
		if isMetaText(text) {
			metaText = true
		}
		parts = append(parts, model.Part{Kind: model.PartText, Text: text})
	}
	for i, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			if isMetaText(b.Text) {
				metaText = true
			}
			parts = append(parts, model.Part{Kind: model.PartText, Text: b.Text})
		case "image":
			if b.Source == nil {
				continue
			}
			parts = append(parts, model.Part{
				Kind: model.PartFile,
				File: &model.FileRef{
					Mime: b.Source.MediaType,
					Ref:  fmt.Sprintf("rec:%d:%d", start, i),
				},
			})
		}
	}
	if len(parts) == 0 {
		return // tool results only, or nothing displayable
	}
	l.msgs = append(l.msgs, model.Message{
		Role:        model.RoleUser,
		Time:        parseTime(rec.Timestamp),
		Parts:       parts,
		IsMeta:      metaText,
		IsSidechain: rec.IsSidechain,
	})
}

func (l *loader) system(rec *rawRecord) {
	if rec.Subtype == "compact_boundary" && rec.CompactMetadata != nil {
		text := fmt.Sprintf("Context compacted (%s · %s → %s tokens)",
			rec.CompactMetadata.Trigger,
			comma(rec.CompactMetadata.PreTokens),
			comma(rec.CompactMetadata.PostTokens))
		msg := model.Message{
			Role: model.RoleSystem,
			Time: parseTime(rec.Timestamp),
			Parts: []model.Part{
				{Kind: model.PartCompaction, Text: text},
			},
		}
		l.msgs = append(l.msgs, msg)
		l.compactMsg, l.compactPart = len(l.msgs)-1, 0
		return
	}
	if strings.TrimSpace(rec.Content) != "" {
		l.msgs = append(l.msgs, model.Message{
			Role: model.RoleSystem,
			Time: parseTime(rec.Timestamp),
			Parts: []model.Part{
				{Kind: model.PartNotice, Text: rec.Content},
			},
		})
	}
}

func (l *loader) attachment(rec *rawRecord) {
	a := rec.Attachment
	if a == nil {
		return
	}
	text := fmt.Sprintf("attachment (%s): %s", a.Type, model.TruncateRunes(a.Rendered, maxAttachmentRunes))
	l.msgs = append(l.msgs, model.Message{
		Role:   model.RoleSystem,
		Time:   parseTime(rec.Timestamp),
		IsMeta: true,
		Parts:  []model.Part{{Kind: model.PartNotice, Text: text}},
	})
}

// clipOutput applies the output size limit and tool-results file reference.
func clipOutput(out string, start int64, toolUseID string) (string, ref string, truncated bool) {
	idx := strings.LastIndex(out, toolResultsMarker)
	if win := strings.LastIndex(out, toolResultsMarkerWindows); win > idx {
		idx = win
	}
	if idx >= 0 {
		name := out[idx+len(toolResultsMarker):]
		if end := strings.IndexAny(name, " \t\r\n\"'`)}]"); end >= 0 {
			name = name[:end]
		}
		if name != "" && !strings.ContainsAny(name, `/\`) && name != "." && name != ".." {
			return out, "file:" + name, true
		}
	}
	if len(out) <= maxOutputBytes {
		return out, "", false
	}
	end := maxOutputBytes
	for end > 0 && !utf8.ValidString(out[:end]) {
		end--
	}
	return out[:end], fmt.Sprintf("rec:%d:%s", start, toolUseID), true
}

// isMetaText reports whether user text is injected context rather than a
// real prompt.
func isMetaText(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "<command-") ||
		strings.HasPrefix(t, "<local-command-") ||
		strings.HasPrefix(t, "Caveat:") ||
		strings.HasPrefix(t, "<system-reminder>")
}

// decodeContent decodes message content, which is either a plain string or an
// array of blocks.
func decodeContent(raw json.RawMessage) (string, []block) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return "", blocks
	}
	return "", nil
}

// contentBlocks returns the block array form of message content.
func contentBlocks(raw json.RawMessage) []block {
	_, blocks := decodeContent(raw)
	return blocks
}

// outputText extracts tool result output from its content (string or blocks).
func outputText(raw json.RawMessage) string {
	text, blocks := decodeContent(raw)
	if text != "" {
		return text
	}
	return joinTextBlocks(blocks)
}

func joinTextBlocks(blocks []block) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" || blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

func parseTime(raw json.RawMessage) time.Time {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// comma formats n with thousands separators (169569 → "169,569").
func comma(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, digit := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Blob returns lazy content for a transcript reference: full tool output
// ("rec:<off>:<toolUseID>"), decoded image bytes ("rec:<off>:<blockIdx>"),
// or a large tool output file ("file:<name>").
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	s, err := p.sourceFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(key, "rec:"):
		offStr, id, ok := strings.Cut(key[len("rec:"):], ":")
		if !ok {
			return nil, fmt.Errorf("claude: malformed blob key %q", key)
		}
		off, err := strconv.ParseInt(offStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("claude: malformed blob key %q", key)
		}
		return p.recordBlob(s.Path, off, id)
	case strings.HasPrefix(key, "file:"):
		name := key[len("file:"):]
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return nil, fmt.Errorf("claude: invalid blob file name %q", name)
		}
		sessionDir := filepath.Join(filepath.Dir(s.Path), strings.TrimSuffix(filepath.Base(s.Path), ".jsonl"))
		if s.ParentID != "" {
			sessionDir = filepath.Dir(filepath.Dir(s.Path))
		}
		data, err := os.ReadFile(filepath.Join(sessionDir, toolResultsDir, name))
		if err != nil {
			return nil, fmt.Errorf("claude: blob %s: %w", key, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("claude: unsupported blob key %q", key)
	}
}

// recordBlob reads the single record at off and extracts the referenced
// tool output or image.
func (p *Provider) recordBlob(path string, off int64, id string) ([]byte, error) {
	r, err := jsonl.Open(path, off)
	if err != nil {
		return nil, fmt.Errorf("claude: blob rec:%d:%s: %w", off, id, err)
	}
	defer func() { _ = r.Close() }()
	line, _, err := r.Next()
	if err != nil {
		return nil, fmt.Errorf("claude: blob rec:%d:%s: %w", off, id, err)
	}
	var rec rawRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil, fmt.Errorf("claude: blob rec:%d:%s: %w", off, id, err)
	}
	if rec.Message == nil {
		return nil, fmt.Errorf("claude: blob rec:%d:%s: no message", off, id)
	}
	blocks := contentBlocks(rec.Message.Content)

	// Numeric id addresses a content block (image).
	if idx, err := strconv.Atoi(id); err == nil {
		if idx < 0 || idx >= len(blocks) {
			return nil, fmt.Errorf("claude: blob rec:%d:%s: block out of range", off, id)
		}
		var rawBlocks []json.RawMessage
		if err := json.Unmarshal(rec.Message.Content, &rawBlocks); err != nil || idx >= len(rawBlocks) {
			return nil, fmt.Errorf("claude: blob rec:%d:%s: decode content: %w", off, id, err)
		}
		var img imageData
		if err := json.Unmarshal(rawBlocks[idx], &img); err != nil || img.Source.Data == "" {
			return nil, fmt.Errorf("claude: blob rec:%d:%s: no image data", off, id)
		}
		data, err := base64.StdEncoding.DecodeString(img.Source.Data)
		if err != nil {
			return nil, fmt.Errorf("claude: blob rec:%d:%s: %w", off, id, err)
		}
		return data, nil
	}

	// Otherwise the id addresses a tool_use call whose result is here.
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID == id {
			return []byte(outputText(b.Content)), nil
		}
	}
	return nil, fmt.Errorf("claude: blob rec:%d:%s: tool result not found", off, id)
}
