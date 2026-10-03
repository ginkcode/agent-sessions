package index

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
)

// SearchFilter narrows full-text search results.
type SearchFilter struct {
	// Agents restricts hits to these agents (empty = all).
	Agents []model.AgentID `json:"agents,omitempty"`
	// Dir is a normalized absolute path prefix; both cwd and repo root are
	// matched at path boundaries.
	Dir string `json:"dir,omitempty"`
	// Limit clamps to 1..100 (default 30).
	Limit int `json:"limit,omitempty"`
}

// SearchHit is one ranked search result.
type SearchHit struct {
	Ref          model.SessionRef `json:"ref"`
	MessageIndex int              `json:"messageIndex"` // -1 = title hit
	Snippet      string           `json:"snippet"`      // HTML-escaped; only <mark> is allowed
	Score        float64          `json:"score"`
	Kind         string           `json:"kind"` // title | text | reasoning | tool
}

// searchLimits bound user input before it reaches SQLite.
const (
	searchMaxInput        = 256 // query string length in runes
	searchMaxTerms        = 20
	searchDefLimit        = 30
	searchMaxLimit        = 100
	searchCandidateFactor = 3
)

// searchQuery is the parsed, safe form of a user query.
type searchQuery struct {
	match    string   // FTS5 MATCH expression built only from quoted tokens
	agents   []string // from agent: prefixes
	dir      string   // from dir: prefixes
	hasTerms bool     // true when at least one FTS token exists
}

// parseSearchQuery converts raw user input into a safe FTS5 MATCH expression.
//
// Grammar:
//   - plain words become ANDed prefix tokens: foo bar → "foo"* AND "bar"*
//   - "quoted phrases" stay exact (no prefix wildcard)
//   - agent:<id> and dir:/path filter prefixes are extracted as parameters
//   - all other FTS syntax (OR, NEAR, -, *, parens) is neutralized because
//     tokens are rebuilt from scratch; embedded quotes are doubled
//
// An empty term list, unbalanced quotes, or input over the limits is a
// validation error — never a raw FTS syntax error from SQLite.
func parseSearchQuery(raw string) (searchQuery, error) {
	runes := []rune(strings.TrimSpace(raw))
	if len(runes) == 0 {
		return searchQuery{}, fmt.Errorf("search: empty query")
	}
	if len(runes) > searchMaxInput {
		return searchQuery{}, fmt.Errorf("search: query too long (max %d characters)", searchMaxInput)
	}

	q := searchQuery{}
	var terms []string
	appendTerm := func(tok string) {
		if tok != "" {
			terms = append(terms, tok)
		}
	}

	for i := 0; i < len(runes); {
		switch {
		case runes[i] == '"':
			// Quoted phrase: scan to the closing quote. An unterminated
			// phrase is a validation error, not silent truncation.
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '"' {
					end = j
					break
				}
			}
			if end < 0 {
				return searchQuery{}, fmt.Errorf("search: unbalanced quotes")
			}
			phrase := strings.TrimSpace(string(runes[i+1 : end]))
			if phrase != "" {
				terms = append(terms, phrase)
				q.hasTerms = true
			}
			i = end + 1

		case runes[i] == ' ' || runes[i] == '\t' || runes[i] == '\n':
			i++

		default:
			// Plain word: runs until whitespace or a quote.
			j := i
			for j < len(runes) && runes[j] != ' ' && runes[j] != '\t' && runes[j] != '\n' && runes[j] != '"' {
				j++
			}
			word := string(runes[i:j])
			i = j
			switch {
			case strings.HasPrefix(word, "agent:"):
				if id := strings.TrimSpace(word[len("agent:"):]); id != "" {
					q.agents = append(q.agents, id)
				}
			case strings.HasPrefix(word, "dir:"):
				if dir := strings.TrimSpace(word[len("dir:"):]); dir != "" {
					q.dir = dir
				}
			default:
				appendTerm(word)
			}
		}
	}

	if len(terms) > searchMaxTerms {
		return searchQuery{}, fmt.Errorf("search: too many terms (max %d)", searchMaxTerms)
	}

	var expr []string
	for _, term := range terms {
		escaped := strings.ReplaceAll(term, `"`, `""`)
		if strings.HasPrefix(term, `"`) || strings.Contains(term, " ") {
			// Phrase-like term: exact match, no prefix wildcard.
			expr = append(expr, `"`+escaped+`"`)
		} else if strings.ContainsAny(term, "*()") || isFTSOperator(term) {
			// A single word that looks like FTS syntax keeps its literal
			// characters but is quoted so SQLite cannot execute it.
			expr = append(expr, `"`+escaped+`"`)
		} else {
			expr = append(expr, `"`+escaped+`"*`)
		}
	}
	if len(expr) == 0 {
		return searchQuery{}, fmt.Errorf("search: no search terms")
	}
	q.match = strings.Join(expr, " AND ")
	return q, nil
}

// isFTSOperator reports whether the word is a bare FTS5 keyword that must
// not be interpreted as syntax.
func isFTSOperator(word string) bool {
	switch strings.ToUpper(word) {
	case "AND", "OR", "NOT", "NEAR":
		return true
	}
	return false
}

// Search runs a safe full-text query over indexed session content.
// Rows are only visible when the session's indexed revision is current
// (fts_indexed_revision = fts_revision), so sessions queued for reindexing
// never surface stale indices. Candidates are fetched at 3× limit, ranked
// in Go with a bounded recency boost, and trimmed to the limit.
func (d *DB) Search(ctx context.Context, query string, filter SearchFilter) ([]SearchHit, error) {
	parsed, err := parseSearchQuery(query)
	if err != nil {
		return nil, err
	}

	agents := filter.Agents
	if len(parsed.agents) > 0 {
		agents = intersectAgents(agents, parsed.agents)
	}
	dir := filter.Dir
	if parsed.dir != "" {
		dir = parsed.dir
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = searchDefLimit
	}
	limit = min(limit, searchMaxLimit)

	if len(agents) == 1 && agents[0] == "" {
		// An explicit agent: filter that resolves to nothing matches no rows.
		return []SearchHit{}, nil
	}

	hits, err := d.searchRows(ctx, parsed.match, agents, dir, limit*searchCandidateFactor)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	type scored struct {
		hit  SearchHit
		rank float64
	}
	ranked := make([]scored, len(hits))
	for i, h := range hits {
		ageDays := now.Sub(time.UnixMilli(h.updatedAt)).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
		recency := 0.1 / (1.0 + ageDays/7.0)
		ranked[i] = scored{hit: h.hit, rank: -h.lexical + recency}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].rank != ranked[j].rank {
			return ranked[i].rank > ranked[j].rank
		}
		// Deterministic ties by ref and message index.
		if ranked[i].hit.Ref.Key() != ranked[j].hit.Ref.Key() {
			return ranked[i].hit.Ref.Key() < ranked[j].hit.Ref.Key()
		}
		return ranked[i].hit.MessageIndex < ranked[j].hit.MessageIndex
	})

	out := make([]SearchHit, 0, limit)
	for _, r := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, r.hit)
	}
	return out, nil
}

type searchRow struct {
	hit       SearchHit
	lexical   float64
	updatedAt int64
}

// searchRows executes the parameterized FTS query. User text only ever
// reaches SQLite as a bound argument.
func (d *DB) searchRows(ctx context.Context, match string, agents []model.AgentID, dir string, limit int) ([]searchRow, error) {
	if d.db == nil {
		return nil, errDBClosed
	}
	if limit <= 0 {
		limit = searchDefLimit
	}

	var sb strings.Builder
	sb.WriteString(`
SELECT d.ref, d.message_index, d.kind,
       snippet(fts_messages, 0, char(1), char(2), '…', 16) AS excerpt,
       bm25(fts_messages) AS lexical, s.updated_at
FROM fts_messages
JOIN fts_docs AS d ON d.rowid = fts_messages.rowid
JOIN sessions AS s ON s.ref = d.ref
WHERE fts_messages MATCH ?
  AND s.fts_revision = s.fts_indexed_revision`)
	args := []any{match}
	if len(agents) > 0 {
		ph := make([]string, len(agents))
		for i, a := range agents {
			ph[i] = "?"
			args = append(args, string(a))
		}
		sb.WriteString(" AND s.agent IN (" + strings.Join(ph, ",") + ")")
	}
	if dir != "" {
		dir = pathutil.Clean(dir)
		prefix, err := dirChildPattern(dir)
		if err != nil {
			return nil, err
		}
		// Windows paths compare case-insensitively (ASCII only, as SQLite
		// folds); POSIX paths exactly, so children match with the
		// case-sensitive GLOB rather than LIKE.
		equal, child := " = ?", " GLOB ?"
		if pathutil.IsWindows(dir) {
			equal, child = " = ? COLLATE NOCASE", " LIKE ? ESCAPE '\\'"
		}
		sb.WriteString(" AND (s.cwd" + equal + " OR s.cwd" + child + " OR s.repo_root" + equal + " OR s.repo_root" + child + ")")
		args = append(args, dir, prefix, dir, prefix)
	}
	sb.WriteString(" ORDER BY lexical ASC, s.updated_at DESC LIMIT ?")
	args = append(args, limit)

	rows, err := d.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("index: search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []searchRow
	for rows.Next() {
		var r searchRow
		var refKey, kind, excerpt string
		if err := rows.Scan(&refKey, &r.hit.MessageIndex, &kind, &excerpt, &r.lexical, &r.updatedAt); err != nil {
			return nil, fmt.Errorf("index: scan search row: %w", err)
		}
		ref, err := parseFTSRef(refKey)
		if err != nil {
			continue
		}
		r.hit.Ref = ref
		r.hit.Kind = kind
		r.hit.Snippet = renderSnippet(excerpt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// dirChildPattern builds a pattern matching the path children of dir: a GLOB
// pattern with *, ? and [ escaped for a POSIX dir, and a LIKE pattern with %,
// _ and \ escaped for a Windows one. A path separator in dir's style precedes
// the trailing wildcard so /repo does not match /repo-other, nor C:\repo
// C:\repo-other.
func dirChildPattern(dir string) (string, error) {
	if !pathutil.IsAbs(dir) {
		return "", fmt.Errorf("search: dir filter must be an absolute path")
	}
	dir = pathutil.Clean(dir)
	sep := pathutil.Separator(dir)
	dir = strings.TrimSuffix(dir, sep) + sep
	if !pathutil.IsWindows(dir) {
		return globEscaper.Replace(dir) + "*", nil
	}
	return likeEscaper.Replace(dir) + "%", nil
}

var (
	globEscaper = strings.NewReplacer(`*`, `[*]`, `?`, `[?]`, `[`, `[[]`)
	likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
)

// renderSnippet converts a snippet() excerpt into safe HTML: the whole text
// is HTML-escaped first, then the sentinel control chars — which cannot
// survive escaping — are replaced with <mark> and </mark>. Only the two
// sentinels can ever introduce markup.
func renderSnippet(excerpt string) string {
	escaped := html.EscapeString(excerpt)
	escaped = strings.ReplaceAll(escaped, "\x01", "<mark>")
	escaped = strings.ReplaceAll(escaped, "\x02", "</mark>")
	return escaped
}

func intersectAgents(filter []model.AgentID, parsed []string) []model.AgentID {
	if len(filter) == 0 {
		filter = []model.AgentID{model.AgentClaude, model.AgentCodex, model.AgentOpenCode}
	}
	var out []model.AgentID
	for _, a := range filter {
		for _, b := range parsed {
			if string(a) == b {
				out = append(out, a)
				break
			}
		}
	}
	return out
}
