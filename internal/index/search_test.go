package index_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func TestSearchQueryParserAndSafety(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := index.Open(ctx, dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-search-1"}
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.Transcripts[ref.ID] = &model.Transcript{
		Meta: model.SessionMeta{
			Ref:       ref,
			Title:     "Debugging WebKit and SQLite locking",
			CWD:       "/home/user/project",
			CreatedAt: time.Now().Add(-2 * time.Hour),
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		},
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "How do I fix SQLite WAL lock with WebKit?"},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "Check if the database has <script>alert('xss')</script> in it."},
					{Kind: model.PartReasoning, Text: "The locking issue occurs because of busy_timeout."},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							ID:    "call_tool",
							Name:  "Bash",
							Input: json.RawMessage(`{"command":"lsof /tmp/db.sqlite"}`),
						},
					},
				},
			},
		},
	}

	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{fake.Transcripts[ref.ID].Meta},
	}); err != nil {
		t.Fatalf("CommitScan: %v", err)
	}
	if err := db.IndexPending(ctx, provider.Set{fake}, nil); err != nil {
		t.Fatalf("IndexPending: %v", err)
	}

	// 1. Prefix query matching "WebKit"
	hits, err := db.Search(ctx, "WebK", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search 'WebK': %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("expected hits for prefix 'WebK'")
	}
	for _, h := range hits {
		if !strings.Contains(h.Snippet, "<mark>") || !strings.Contains(h.Snippet, "</mark>") {
			t.Errorf("snippet missing mark tags: %q", h.Snippet)
		}
	}

	// 2. Exact phrase query
	hits, err = db.Search(ctx, `"SQLite WAL"`, index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search phrase: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("expected hit for exact phrase '\"SQLite WAL\"'")
	}

	// 3. FTS operator injection safety (NEAR, OR, *, parenthesis should not crash or execute syntax)
	badQueries := []string{
		"WebKit OR SQLite",
		"WebKit NEAR SQLite",
		"SQLite *",
		"(WebKit AND SQLite)",
		"WebKit AND",
	}
	for _, bq := range badQueries {
		_, err := db.Search(ctx, bq, index.SearchFilter{})
		if err != nil {
			t.Errorf("Search query %q failed: %v", bq, err)
		}
	}

	// 4. Validation errors: unbalanced quotes or empty query
	_, err = db.Search(ctx, `"unbalanced phrase`, index.SearchFilter{})
	if err == nil {
		t.Errorf("expected error for unbalanced quotes, got nil")
	}
	_, err = db.Search(ctx, "   ", index.SearchFilter{})
	if err == nil {
		t.Errorf("expected error for whitespace query, got nil")
	}

	// 5. HTML escaping & safe snippet (<script> must stay escaped)
	hits, err = db.Search(ctx, "alert", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search 'alert': %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("expected hit for 'alert'")
	}
	for _, h := range hits {
		if strings.Contains(h.Snippet, "<script>") {
			t.Errorf("raw <script> found unescaped in snippet: %s", h.Snippet)
		}
		if !strings.Contains(h.Snippet, "&lt;script&gt;") {
			t.Errorf("expected escaped &lt;script&gt; in snippet: %s", h.Snippet)
		}
	}

	// 6. Inline agent and dir filters: agent:claude-code
	hits, err = db.Search(ctx, "SQLite agent:claude-code", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search with agent: filter: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("expected hit with agent:claude-code")
	}
	hits, err = db.Search(ctx, "SQLite agent:codex", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search with non-matching agent: filter: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for agent:codex, got %d", len(hits))
	}

	// 7. Directory filter matching cwd
	hits, err = db.Search(ctx, "SQLite", index.SearchFilter{Dir: "/home/user/project"})
	if err != nil {
		t.Fatalf("Search with Dir filter: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("expected hit with matching Dir filter")
	}
	hits, err = db.Search(ctx, "SQLite", index.SearchFilter{Dir: "/other/dir"})
	if err != nil {
		t.Fatalf("Search with non-matching Dir filter: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits with non-matching Dir, got %d", len(hits))
	}
}

func TestSearchVisibilityGuard(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := index.Open(ctx, dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-vis"}
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.Transcripts[ref.ID] = &model.Transcript{
		Meta: model.SessionMeta{Ref: ref, Title: "Visible Session Title"},
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "UniqueKeywordForVisibility"}}},
		},
	}

	// CommitScan enqueues job and bumps fts_revision, but fts_indexed_revision is still 0
	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{fake.Transcripts[ref.ID].Meta},
	}); err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	// Search before indexing: must return 0 hits because fts_indexed_revision != fts_revision
	hits, err := db.Search(ctx, "UniqueKeywordForVisibility", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits before indexing, got %d", len(hits))
	}

	// Index pending
	if err := db.IndexPending(ctx, provider.Set{fake}, nil); err != nil {
		t.Fatalf("IndexPending: %v", err)
	}

	// Now it should be visible
	hits, err = db.Search(ctx, "UniqueKeywordForVisibility", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search after indexing: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit after indexing, got %d", len(hits))
	}

	// Modify session without indexing: should become hidden again until re-indexed
	fake.Transcripts[ref.ID].Meta.Title = "Updated Title"
	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{fake.Transcripts[ref.ID].Meta},
	}); err != nil {
		t.Fatalf("CommitScan 2: %v", err)
	}

	hits, err = db.Search(ctx, "UniqueKeywordForVisibility", index.SearchFilter{})
	if err != nil {
		t.Fatalf("Search after rev bump: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits while re-indexing, got %d", len(hits))
	}
}
