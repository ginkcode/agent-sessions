package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

const (
	indexID   = "11111111-1111-1111-1111-111111111111"
	childID   = "22222222-2222-2222-2222-222222222222"
	orphanID  = "33333333-3333-3333-3333-333333333333"
	missingID = "44444444-4444-4444-4444-444444444444"
	outsideID = "55555555-5555-5555-5555-555555555555"
)

// Synthetic SQL only. No real Codex database or authentication file is used.
const stateSQL = `
CREATE TABLE threads (
 id TEXT PRIMARY KEY, rollout_path TEXT, cwd TEXT, title TEXT,
 first_user_message TEXT, preview TEXT, model TEXT, tokens_used INTEGER,
 archived INTEGER, git_branch TEXT, cli_version TEXT,
 created_at_ms INTEGER, updated_at_ms INTEGER,
 created_at INTEGER, updated_at INTEGER
);
CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT);
`

func stateDB(t *testing.T, root, filename, schema string) *sql.DB {
	t.Helper()
	path := filepath.Join(root, filename)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("initializing synthetic index: %v", err)
	}
	return db
}

func indexRollout(t *testing.T, root, id string, archived bool) string {
	t.Helper()
	content := line("2026-09-28T12:00:00Z", "session_meta", `{"id":"`+id+`","cwd":"/tmp/rollout-cwd","cli_version":"0.1"}`) +
		line("2026-09-28T12:00:01Z", "response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"From rollout"}]}`) +
		line("2026-09-28T12:00:02Z", "response_item", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Reply"}]}`) +
		line("2026-09-28T12:00:03Z", "response_item", `{"type":"function_call","call_id":"tool_1","name":"shell"}`) +
		line("2026-09-28T12:00:04Z", "event_msg", `{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":20,"cached_input_tokens":3,"reasoning_output_tokens":4}}}`)
	return writeRollout(t, root, id, content, archived)
}

func insertThread(t *testing.T, db *sql.DB, id, path, title string, archived int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO threads (id, rollout_path, cwd, title, first_user_message,
model, tokens_used, archived, git_branch, cli_version, created_at_ms, updated_at_ms, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, path, "/tmp/index-cwd", title, "Indexed first prompt", "gpt-index", 99999, archived,
		"index-branch", "0.index", 1789900000123, 1789900001123, 123, 456)
	if err != nil {
		t.Fatal(err)
	}
}

func warningContains(d provider.Diagnostics, text string) bool {
	for _, w := range d.Warnings {
		if strings.Contains(w.Msg, text) {
			return true
		}
	}
	return false
}

func metaByID(t *testing.T, res provider.ScanResult, id string) model.SessionMeta {
	t.Helper()
	for _, m := range res.Changed {
		if m.Ref.ID == id {
			return m
		}
	}
	t.Fatalf("no changed session %q in %+v", id, res.Changed)
	return model.SessionMeta{}
}

func TestScanIndex_PopulatedMergesCountsAndAncestry(t *testing.T) {
	root := t.TempDir()
	parentPath := indexRollout(t, root, indexID, false)
	childPath := indexRollout(t, root, childID, true)
	unindexed := indexRollout(t, root, orphanID, false)
	db := stateDB(t, root, "state_10.sqlite", stateSQL)
	insertThread(t, db, indexID, parentPath, "Indexed title", 0)
	insertThread(t, db, childID, childPath, "Child title", 1)
	if _, err := db.Exec(`INSERT INTO thread_spawn_edges VALUES (?,?),(?,?),(?,?)`, indexID, childID, indexID, missingID, missingID, orphanID); err != nil {
		t.Fatal(err)
	}
	// The lower-version index is populated but must not be opened.
	older := stateDB(t, root, "state_2.sqlite", stateSQL)
	insertThread(t, older, orphanID, unindexed, "Wrong older title", 0)

	res, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 3 {
		t.Fatalf("Changed=%d, want 3: %+v", len(res.Changed), res.Changed)
	}
	parent := metaByID(t, res, indexID)
	if parent.Title != "Indexed title" || parent.FirstPrompt != "Indexed first prompt" || parent.CWD != "/tmp/index-cwd" || parent.Model != "gpt-index" || parent.AgentVersion != "0.index" || parent.GitBranch != "index-branch" {
		t.Errorf("index metadata not merged: %+v", parent)
	}
	if parent.CreatedAt != time.UnixMilli(1789900000123).UTC() || parent.UpdatedAt != time.UnixMilli(1789900001123).UTC() {
		t.Errorf("ms timestamps not preferred over seconds: %v/%v", parent.CreatedAt, parent.UpdatedAt)
	}
	if parent.Counts != (model.MessageCounts{User: 1, Assistant: 1, ToolCalls: 1}) || parent.Tokens != (model.TokenUsage{Input: 10, Output: 20, CacheRead: 3, Reasoning: 4}) {
		t.Errorf("counts/usage must come from rollout (not tokens_used): %+v/%+v", parent.Counts, parent.Tokens)
	}
	if child := metaByID(t, res, childID); child.ParentID != indexID || !child.Archived || child.SourcePath != childPath {
		t.Errorf("child edge/archive/path = %+v", child)
	}
	if extra := metaByID(t, res, orphanID); extra.Title != "From rollout" || extra.ParentID != "" {
		t.Errorf("unindexed rollout or dangling edge was mishandled: %+v", extra)
	}
}

func TestScanIndex_EdgeChangeOnUnchangedRollouts(t *testing.T) {
	root := t.TempDir()
	parent := indexRollout(t, root, indexID, false)
	child := indexRollout(t, root, childID, false)
	db := stateDB(t, root, "state_5.sqlite", stateSQL)
	insertThread(t, db, indexID, parent, "Parent", 0)
	insertThread(t, db, childID, child, "Child", 0)
	if _, err := db.Exec(`INSERT INTO thread_spawn_edges VALUES (?,?)`, indexID, childID); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if got := metaByID(t, first, childID).ParentID; got != indexID {
		t.Fatalf("initial edge = %q, want %q", got, indexID)
	}
	if _, err := db.Exec(`DELETE FROM thread_spawn_edges`); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changed) != 1 || second.Changed[0].Ref.ID != childID || second.Changed[0].ParentID != "" {
		t.Errorf("edge deletion on unchanged files must update child: %+v", second.Changed)
	}
}

func TestScanIndex_ChangedTitleOnUnchangedRolloutAndFallback(t *testing.T) {
	root := t.TempDir()
	path := indexRollout(t, root, indexID, false)
	db := stateDB(t, root, "state_5.sqlite", stateSQL)
	insertThread(t, db, indexID, path, "First index title", 0)
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE threads SET title = 'Updated index title' WHERE id = ?`, indexID); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changed) != 1 || second.Changed[0].Title != "Updated index title" {
		t.Fatalf("unchanged rollout did not emit changed index title: %+v", second.Changed)
	}
	third, err := p.Scan(context.Background(), second.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Changed) != 0 {
		t.Errorf("unchanged index/file emitted Changed: %+v", third.Changed)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "state_5.sqlite")); err != nil {
		t.Fatal(err)
	}
	fallback, err := p.Scan(context.Background(), third.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback.Changed) != 1 || fallback.Changed[0].Title != "From rollout" || len(fallback.Removed) != 0 || !warningContains(fallback.Diag, "index missing") {
		t.Errorf("index disappearance should change metadata but not remove rollout: %+v", fallback)
	}
}

func TestScanIndex_EmptyMissingAndCorruptFallback(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "corrupt", "missing-table"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			indexRollout(t, root, indexID, false)
			switch mode {
			case "empty":
				stateDB(t, root, "state_5.sqlite", stateSQL)
			case "corrupt":
				if err := os.WriteFile(filepath.Join(root, "state_5.sqlite"), []byte("not sqlite"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing-table":
				stateDB(t, root, "state_5.sqlite", "CREATE TABLE unrelated (id TEXT)")
			}
			res, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Changed) != 1 || res.Changed[0].Counts.User != 1 || len(res.Diag.Warnings) == 0 {
				t.Errorf("%s fallback: %+v", mode, res)
			}
		})
	}
}

func TestScanIndex_LockedFallback(t *testing.T) {
	root := t.TempDir()
	indexRollout(t, root, indexID, false)
	db := stateDB(t, root, "state_5.sqlite", stateSQL)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(context.Background(), `BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), `ROLLBACK`) })
	res, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || res.Changed[0].Counts.User != 1 || !warningContains(res.Diag, "index unavailable") {
		t.Errorf("locked index did not fall back to rollout: %+v", res)
	}
}

func TestScanIndex_OptionalColumnsSecondsFallback(t *testing.T) {
	root := t.TempDir()
	path := indexRollout(t, root, indexID, false)
	db := stateDB(t, root, "state_1.sqlite", `CREATE TABLE threads (id TEXT, rollout_path TEXT, title TEXT, created_at INTEGER, updated_at INTEGER)`)
	if _, err := db.Exec(`INSERT INTO threads VALUES (?,?,?,?,?)`, indexID, path, "Older title", int64(100), int64(200)); err != nil {
		t.Fatal(err)
	}
	res, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	meta := metaByID(t, res, indexID)
	if meta.Title != "Older title" || meta.CreatedAt != time.Unix(100, 0).UTC() || meta.UpdatedAt != time.Unix(200, 0).UTC() || meta.Archived {
		t.Errorf("optional columns / seconds fallback: %+v", meta)
	}
	if meta.Counts.User != 1 || meta.Tokens.Input != 10 {
		t.Errorf("missing optional columns lost rollout detail: %+v", meta)
	}
}

func TestScanIndex_DuplicateIDUsesMatchingPathAndNoDuplicate(t *testing.T) {
	root := t.TempDir()
	indexRollout(t, root, indexID, false)
	archived := indexRollout(t, root, indexID, true)
	db := stateDB(t, root, "state_5.sqlite", stateSQL)
	insertThread(t, db, indexID, archived, "Archived index", 1)
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Changed) != 1 || !first.Changed[0].Archived || first.Changed[0].SourcePath != archived {
		t.Fatalf("index path did not win duplicate ID: %+v", first.Changed)
	}
	if err := os.Remove(archived); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Removed) != 0 || len(second.Changed) != 1 || second.Changed[0].Archived || !warningContains(second.Diag, "missing or escapes") {
		t.Errorf("archived path deletion should switch to active path once: %+v", second)
	}
}

func TestScanIndex_MissingFileNotVisibleAndPathEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsidePath := indexRollout(t, outside, outsideID, false)
	db := stateDB(t, root, "state_5.sqlite", stateSQL)
	insertThread(t, db, missingID, filepath.Join(root, "sessions", "missing", "rollout-nope.jsonl"), "Ghost", 0)
	insertThread(t, db, outsideID, outsidePath, "External", 0)
	res, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 0 || len(res.State.Sources) != 0 || !warningContains(res.Diag, "outside Codex root") {
		t.Errorf("missing/external rows should not be visible or opened: %+v", res)
	}
	// A row with a syntactically valid path cannot make an old session visible
	// after its rollout disappears.
	path := indexRollout(t, root, indexID, false)
	insertThread(t, db, indexID, path, "To vanish", 0)
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changed) != 0 || len(second.Removed) != 1 || second.Removed[0].ID != indexID {
		t.Errorf("vanished indexed rollout not removed: %+v", second)
	}
}

func TestScanIndex_SymlinkEscapeAndExtraExplicitSource(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsidePath := indexRollout(t, outside, outsideID, false)
	link := filepath.Join(root, "sessions", "2026", "09", "28", "rollout-linked.jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	db := stateDB(t, root, "state_1.sqlite", stateSQL)
	insertThread(t, db, outsideID, link, "Escape", 0)
	res, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 0 || !warningContains(res.Diag, "escapes") {
		t.Errorf("symlink escape not rejected: %+v", res)
	}
	// A valid explicit file outside the normal discovery shape is safe to scan.
	explicit := filepath.Join(root, "custom", "rollout-explicit.jsonl")
	if err := os.MkdirAll(filepath.Dir(explicit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, []byte(line("2026-09-28T12:00:00Z", "session_meta", `{"id":"`+indexID+`"}`)), 0o644); err != nil {
		t.Fatal(err)
	}
	insertThread(t, db, indexID, filepath.Join("custom", "rollout-explicit.jsonl"), "Explicit", 0)
	res, err = New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || res.Changed[0].Title != "Explicit" || res.Changed[0].SourcePath != explicit {
		t.Errorf("valid explicit index source not scanned: %+v", res)
	}
}
