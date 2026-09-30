package index_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func TestFTSTextExtractionAndCaps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := index.Open(ctx, dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "test-sess"}
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	largeText := strings.Repeat("hello world ", 1000) // ~12 KiB
	tr := &model.Transcript{
		Meta: model.SessionMeta{
			Ref:   ref,
			Title: "Test Session With Long Title " + strings.Repeat("A", 2000),
		},
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "Initial prompt"},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartText, Text: largeText},
					{Kind: model.PartReasoning, Text: "Private reasoning step"},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							ID:    "call_1",
							Name:  "Bash",
							Input: json.RawMessage(`{"command":"ls -la /tmp","ignored_extra":123}`),
						},
					},
				},
			},
			{
				Role:   model.RoleSystem,
				IsMeta: true,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "System hidden instruction"},
				},
			},
		},
	}
	fake.Transcripts[ref.ID] = tr

	// Enqueue via CommitScan
	scanRes := provider.ScanResult{
		Changed: []model.SessionMeta{tr.Meta},
	}
	if err := db.CommitScan(ctx, model.AgentClaude, scanRes); err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	var progress []index.FTSProgress
	var mu sync.Mutex
	emit := func(p index.FTSProgress) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, p)
	}

	if err := db.IndexPending(ctx, provider.Set{fake}, emit); err != nil {
		t.Fatalf("IndexPending: %v", err)
	}

	// Verify docs in DB
	rows, err := db.SQLDB().QueryContext(ctx, "SELECT message_index, kind, length(body) FROM fts_docs WHERE ref = ? ORDER BY message_index", ref.Key())
	if err != nil {
		t.Fatalf("Query docs: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type docRow struct {
		index int
		kind  string
		len   int
	}
	var docs []docRow
	for rows.Next() {
		var d docRow
		if err := rows.Scan(&d.index, &d.kind, &d.len); err != nil {
			t.Fatalf("Scan doc: %v", err)
		}
		docs = append(docs, d)
	}

	// Expected:
	// -1: title (capped at 1024 bytes)
	// 0: user message (kind "text")
	// 1: assistant message (kind "text", capped at 8192 bytes)
	// System message (index 2) is skipped because IsMeta / RoleSystem
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d: %+v", len(docs), docs)
	}
	if docs[0].index != -1 || docs[0].kind != "title" || docs[0].len > 1024 {
		t.Errorf("title doc invalid: %+v", docs[0])
	}
	if docs[1].index != 0 || docs[1].kind != "text" {
		t.Errorf("doc 0 invalid: %+v", docs[1])
	}
	if docs[2].index != 1 || docs[2].kind != "text" || docs[2].len > 8192 {
		t.Errorf("doc 1 invalid: %+v", docs[2])
	}

	// Verify fts_messages trigger populated external content
	var count int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_messages WHERE fts_messages MATCH 'prompt'").Scan(&count); err != nil {
		t.Fatalf("MATCH query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 match for prompt, got %d", count)
	}

	// Verify sessions.fts_indexed_revision was updated
	var indexedRev, rev int64
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT fts_revision, fts_indexed_revision FROM sessions WHERE ref = ?", ref.Key()).Scan(&rev, &indexedRev); err != nil {
		t.Fatalf("Query sessions: %v", err)
	}
	if indexedRev != rev || indexedRev == 0 {
		t.Errorf("expected indexedRev == rev > 0, got %d vs %d", indexedRev, rev)
	}
}

func TestFTSWorkerDebounceAndStaleRevision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	db, err := index.Open(ctx, dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-stale"}
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.Transcripts[ref.ID] = &model.Transcript{
		Meta: model.SessionMeta{Ref: ref, Title: "Rev 1"},
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "First prompt"}}},
		},
	}

	worker := index.StartIndexer(ctx, db, provider.Set{fake}, nil)
	defer worker.Close()

	// Commit initial scan
	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{fake.Transcripts[ref.ID].Meta},
	}); err != nil {
		t.Fatalf("CommitScan 1: %v", err)
	}
	worker.Notify()

	// Wait for indexing to complete
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var pending int
		_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_jobs").Scan(&pending)
		if pending == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	var matchCount int
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_messages WHERE fts_messages MATCH 'First'").Scan(&matchCount)
	if matchCount != 1 {
		t.Fatalf("expected 1 match for 'First', got %d", matchCount)
	}

	// Update transcript to Rev 2 and commit new scan
	fake.Transcripts[ref.ID] = &model.Transcript{
		Meta: model.SessionMeta{Ref: ref, Title: "Rev 2"},
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "Second prompt replaced"}}},
		},
	}
	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{fake.Transcripts[ref.ID].Meta},
	}); err != nil {
		t.Fatalf("CommitScan 2: %v", err)
	}
	worker.Notify()

	// Wait for replacement
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var pending int
		_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_jobs").Scan(&pending)
		if pending == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Old text should be gone, new text should match
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_messages WHERE fts_messages MATCH 'First'").Scan(&matchCount)
	if matchCount != 0 {
		t.Errorf("expected 0 match for old 'First', got %d", matchCount)
	}
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_messages WHERE fts_messages MATCH 'Second'").Scan(&matchCount)
	if matchCount != 1 {
		t.Errorf("expected 1 match for 'Second', got %d", matchCount)
	}

	// Delete session and ensure FTS cascades
	if err := db.DeleteSessions(ctx, []model.SessionRef{ref}); err != nil {
		t.Fatalf("DeleteSessions: %v", err)
	}

	var docCount int
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_docs WHERE ref = ?", ref.Key()).Scan(&docCount)
	if docCount != 0 {
		t.Errorf("expected 0 fts_docs after delete, got %d", docCount)
	}
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fts_messages WHERE fts_messages MATCH 'Second'").Scan(&matchCount)
	if matchCount != 0 {
		t.Errorf("expected 0 matches in fts_messages after delete, got %d", matchCount)
	}
}

func TestFTSFailedJobBackoff(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := index.Open(ctx, dir, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-fail"}
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.LoadErr = fmt.Errorf("simulated disk read failure")

	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{{Ref: ref, Title: "Failing Session"}},
	}); err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	err = db.IndexPending(ctx, provider.Set{fake}, nil)
	if err == nil {
		t.Fatalf("expected error from IndexPending, got nil")
	}

	var attempts int
	var lastErr string
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT attempts, last_error FROM fts_jobs WHERE ref = ?", ref.Key()).Scan(&attempts, &lastErr); err != nil {
		t.Fatalf("Query fts_jobs: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected attempts == 1, got %d", attempts)
	}
	if !strings.Contains(lastErr, "simulated disk read failure") {
		t.Errorf("unexpected last_error: %q", lastErr)
	}
}
