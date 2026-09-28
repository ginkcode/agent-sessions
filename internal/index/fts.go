package index

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// FTSProgress reports background indexing status to the UI.
type FTSProgress struct {
	Done    int  `json:"done"`    // sessions whose indexed revision is current
	Pending int  `json:"pending"` // queued fts_jobs rows
	Failed  int  `json:"failed"`  // jobs that have recorded at least one error
	Running bool `json:"running"`
}

// ftsDoc is one row of fts_docs: the title row (messageIndex -1) or the
// combined searchable text of one message.
type ftsDoc struct {
	Ref          string
	MessageIndex int
	Kind         string // title | text | reasoning | tool
	Body         string
}

// Indexing caps: bounded UTF-8 sizes keep giant tool inputs and long
// reasoning runs from dominating the FTS index.
const (
	titleCapBytes    = 1 << 10 // 1 KiB
	messageCapBytes  = 8 << 10 // 8 KiB
	toolSummaryCap   = 512     // plain-text tool input summary
	snippetSentinel1 = '\x01'  // snippet() match-open marker
	snippetSentinel2 = '\x02'  // snippet() match-close marker
)

// buildFTSDocs extracts the searchable rows of one transcript. The title
// row comes first (messageIndex -1); each visible message contributes one
// row at its zero-based Messages index. Meta and system messages are not
// indexed (the transcript viewer hides them too), and tool outputs,
// patches and file attachments are excluded: only visible text, reasoning
// text, and a short tool name/input summary are searchable.
func buildFTSDocs(ref string, tr *model.Transcript) []ftsDoc {
	docs := make([]ftsDoc, 0, len(tr.Messages)+1)
	if body := capText(tr.Meta.Title, titleCapBytes); body != "" {
		docs = append(docs, ftsDoc{Ref: ref, MessageIndex: -1, Kind: "title", Body: body})
	}
	for i := range tr.Messages {
		msg := &tr.Messages[i]
		if msg.IsMeta || msg.Role == model.RoleSystem {
			continue
		}
		body, kind := extractMessageText(msg)
		if body == "" {
			continue
		}
		docs = append(docs, ftsDoc{Ref: ref, MessageIndex: i, Kind: kind, Body: body})
	}
	return docs
}

// extractMessageText returns the message's combined searchable text and its
// row kind. Ordinary text is preferred over reasoning, which is preferred
// over the tool summary, both for the kind label and for filling the 8 KiB
// cap.
func extractMessageText(msg *model.Message) (string, string) {
	var text, reasoning, tools strings.Builder
	hasText, hasReasoning := false, false
	toolCount := 0
	for i := range msg.Parts {
		part := &msg.Parts[i]
		switch part.Kind {
		case model.PartText:
			if part.Text == "" {
				continue
			}
			hasText = true
			appendSep(&text)
			text.WriteString(capText(part.Text, messageCapBytes))
		case model.PartReasoning:
			if part.Text == "" {
				continue
			}
			hasReasoning = true
			appendSep(&reasoning)
			reasoning.WriteString(capText(part.Text, messageCapBytes))
		case model.PartTool:
			if part.Tool == nil {
				continue
			}
			toolCount++
			appendSep(&tools)
			tools.WriteString(capText(toolSummary(part.Tool), messageCapBytes))
		default:
			// Patches, files, compaction and notice parts are not indexed.
		}
	}
	if toolCount == 0 && !hasText && !hasReasoning {
		return "", ""
	}

	kind := "tool"
	switch {
	case hasText:
		kind = "text"
	case hasReasoning:
		kind = "reasoning"
	}

	body := joinCapped([]string{text.String(), reasoning.String(), tools.String()}, messageCapBytes)
	return body, kind
}

// toolSummary renders a tool call as its name plus a short plain-text
// summary of the input JSON: string, number and boolean values only, no
// keys, no punctuation, capped at toolSummaryCap bytes.
func toolSummary(tool *model.ToolCall) string {
	var b strings.Builder
	b.WriteString(tool.Name)
	if summary := jsonPlainValues(tool.Input, toolSummaryCap); summary != "" {
		b.WriteByte(' ')
		b.WriteString(summary)
	}
	return b.String()
}

// jsonPlainValues walks JSON and keeps only scalar values (dropping object
// keys), returning at most max bytes of space-separated plain text.
func jsonPlainValues(raw json.RawMessage, max int) string {
	if len(raw) == 0 || max <= 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	// Each stack frame tracks whether the container is an object (whose
	// string tokens alternate key, value) or an array.
	var out strings.Builder
	type frame struct {
		object    bool
		expectKey bool
	}
	stack := make([]frame, 0, 8)
	emit := func(s string) {
		if s == "" {
			return
		}
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(s)
	}

	for out.Len() < max {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, frame{object: true, expectKey: true})
			case '[':
				stack = append(stack, frame{object: false})
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				// A completed object/array value satisfies the enclosing
				// object's value slot.
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectKey = true
				}
			}
		case string:
			if len(stack) > 0 {
				top := &stack[len(stack)-1]
				if top.object {
					if top.expectKey {
						top.expectKey = false
						continue // key, not a value
					}
					top.expectKey = true
				}
			}
			emit(t)
		default: // json.Number, bool, nil
			if len(stack) > 0 && stack[len(stack)-1].object {
				stack[len(stack)-1].expectKey = true
			}
			emit(tokString(tok))
		}
	}
	return capText(out.String(), max)
}

func tokString(tok any) string {
	switch t := tok.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func appendSep(b *strings.Builder) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
}

// joinCapped concatenates non-empty parts (separated by single spaces) until
// cap bytes, truncating the last fitting part at a UTF-8 boundary.
func joinCapped(parts []string, cap int) string {
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if b.Len() == 0 {
			if len(p) <= cap {
				b.WriteString(p)
				continue
			}
			b.WriteString(truncateUTF8(p, cap))
			break
		}
		remaining := cap - b.Len() - 1 // room for the separator space
		if remaining <= 0 {
			break
		}
		b.WriteByte(' ')
		if len(p) <= remaining {
			b.WriteString(p)
			continue
		}
		b.WriteString(truncateUTF8(p, remaining))
		break
	}
	return b.String()
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// capText normalizes and truncates a block of text for indexing: sentinels
// stripped (they carry snippet() marks), whitespace collapsed, capped at
// max bytes on a UTF-8 boundary.
func capText(s string, max int) string {
	s = stripSentinels(s)
	s = model.OneLine(s)
	return truncateUTF8(s, max)
}

func stripSentinels(s string) string {
	if !strings.ContainsRune(s, snippetSentinel1) && !strings.ContainsRune(s, snippetSentinel2) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == snippetSentinel1 || r == snippetSentinel2 {
			return -1
		}
		return r
	}, s)
}

// Progress snapshots the indexing queue and completion counters.
func (d *DB) Progress(ctx context.Context) (FTSProgress, error) {
	return d.ftsProgress(ctx, false)
}

// ftsProgress snapshots the indexing queue and completion counters.
func (d *DB) ftsProgress(ctx context.Context, running bool) (FTSProgress, error) {
	if d.db == nil {
		return FTSProgress{}, errDBClosed
	}
	var p FTSProgress
	err := d.db.QueryRowContext(ctx, `
SELECT
    (SELECT COUNT(*) FROM sessions WHERE fts_revision = fts_indexed_revision),
    (SELECT COUNT(*) FROM fts_jobs),
    (SELECT COUNT(*) FROM fts_jobs WHERE attempts > 0);
`).Scan(&p.Done, &p.Pending, &p.Failed)
	if err != nil {
		return FTSProgress{}, err
	}
	p.Running = running
	return p, nil
}
