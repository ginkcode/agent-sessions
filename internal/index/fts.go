package index

import (
	"context"
	"strings"

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
	return model.ExtractMessageText(msg, messageCapBytes)
}

// toolSummary renders a tool call as its name plus a short plain-text
// summary of the input JSON: string, number and boolean values only, no
// keys, no punctuation, capped at toolSummaryCap bytes.
func toolSummary(tool *model.ToolCall) string {
	return model.ToolSummary(tool)
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	return model.TruncateUTF8(s, n)
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
