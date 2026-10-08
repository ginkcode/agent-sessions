package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
	"github.com/ginkcode/agent-sessions/internal/testutil/golden"

	_ "modernc.org/sqlite"
)

// scanFixture is a synthetic v2 store. All rows are test data; the real
// OpenCode store is never opened by these tests.
type scanFixture struct {
	t    *testing.T
	root string
	db   *sql.DB
}

func newScanFixture(t *testing.T) *scanFixture {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema := `
CREATE TABLE session_v2 (
	id TEXT PRIMARY KEY,
	project_id TEXT,
	parent_id TEXT,
	directory TEXT,
	title TEXT,
	version TEXT,
	agent TEXT,
	model TEXT,
	fork_session_id TEXT,
	commit_hash TEXT,
	cost REAL,
	tokens_input INTEGER,
	tokens_output INTEGER,
	tokens_reasoning INTEGER,
	tokens_cache_read INTEGER,
	tokens_cache_write INTEGER,
	time_created INTEGER,
	time_updated INTEGER,
	time_archived INTEGER
);
CREATE TABLE session_message (
	id INTEGER PRIMARY KEY,
	session_id TEXT,
	type TEXT,
	seq INTEGER,
	time_created INTEGER,
	data TEXT
);
CREATE TABLE session (
	id TEXT PRIMARY KEY,
	project_id TEXT,
	parent_id TEXT,
	directory TEXT,
	title TEXT,
	version TEXT,
	agent TEXT,
	model TEXT,
	cost REAL,
	tokens_input INTEGER,
	tokens_output INTEGER,
	tokens_reasoning INTEGER,
	tokens_cache_read INTEGER,
	tokens_cache_write INTEGER,
	time_created INTEGER,
	time_updated INTEGER,
	time_archived INTEGER
);
CREATE TABLE message (
	id TEXT PRIMARY KEY,
	session_id TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
CREATE TABLE part (
	id TEXT PRIMARY KEY,
	message_id TEXT,
	session_id TEXT,
	time_created INTEGER,
	time_updated INTEGER,
	data TEXT
);
CREATE TABLE todo (session_id TEXT, content TEXT, status TEXT, priority TEXT, position INTEGER);
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("fixture schema: %v", err)
	}
	return &scanFixture{t: t, root: root, db: db}
}

func (f *scanFixture) session(id string, overrides map[string]any) {
	f.t.Helper()
	columns := map[string]any{
		"id":           id,
		"project_id":   "proj1",
		"directory":    "/repo/work",
		"title":        "Session " + id,
		"version":      "2.0.18",
		"agent":        "build",
		"model":        `{"providerID":"anthropic","id":"claude-sonnet-4"}`,
		"time_created": int64(1_700_000_000_000),
		"time_updated": int64(1_700_000_100_000),
	}
	for k, v := range overrides {
		columns[k] = v
	}
	var names, marks []string
	var args []any
	for name, value := range columns {
		names = append(names, name)
		marks = append(marks, "?")
		args = append(args, value)
	}
	query := "INSERT INTO session_v2 (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("fixture session %s: %v", id, err)
	}
}

func (f *scanFixture) v1Session(id string, overrides map[string]any) {
	f.t.Helper()
	columns := map[string]any{
		"id": id, "project_id": "proj1", "directory": "/repo/work", "title": "Session " + id,
		"version": "1.18.33", "agent": "build", "model": `{"providerID":"anthropic","id":"claude-sonnet-4"}`,
		"time_created": int64(1_700_000_000_000), "time_updated": int64(1_700_000_100_000),
	}
	for key, value := range overrides {
		columns[key] = value
	}
	var names, marks []string
	var args []any
	for name, value := range columns {
		names, marks, args = append(names, name), append(marks, "?"), append(args, value)
	}
	if _, err := f.db.Exec("INSERT INTO session ("+strings.Join(names, ",")+") VALUES ("+strings.Join(marks, ",")+")", args...); err != nil {
		f.t.Fatalf("fixture v1 session %s: %v", id, err)
	}
}

func (f *scanFixture) v1Message(id, sessionID, role string, created int64) {
	f.t.Helper()
	data := fmt.Sprintf(`{"role":%q,"time":{"created":%d}}`, role, created)
	if _, err := f.db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`, id, sessionID, created, created, data); err != nil {
		f.t.Fatalf("fixture v1 message: %v", err)
	}
}

func (f *scanFixture) v1Part(id, messageID, sessionID string, created int64, data string) {
	f.t.Helper()
	if _, err := f.db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`, id, messageID, sessionID, created, created, data); err != nil {
		f.t.Fatalf("fixture v1 part: %v", err)
	}
}

func (f *scanFixture) message(sessionID, kind string, seq int, data string) {
	f.t.Helper()
	if _, err := f.db.Exec(`INSERT INTO session_message (session_id, type, seq, time_created, data)
		VALUES (?, ?, ?, ?, ?)`, sessionID, kind, seq, int64(1_700_000_050_000), data); err != nil {
		f.t.Fatalf("fixture message: %v", err)
	}
}

func (f *scanFixture) project(id, worktree string) {
	f.t.Helper()
	if _, err := f.db.Exec(`INSERT INTO project (id, worktree) VALUES (?, ?)`, id, worktree); err != nil {
		f.t.Fatalf("fixture project: %v", err)
	}
}

// messageData builds an assistant row whose content array has n tool items.
func messageData(text string, tools int) string {
	data := map[string]any{"text": text}
	if tools > 0 {
		content := []any{map[string]any{"type": "text", "text": "thinking"}}
		for i := 0; i < tools; i++ {
			content = append(content, map[string]any{
				"type": "tool",
				"id":   fmt.Sprintf("tool-%d", i),
				"call": "Read",
			})
		}
		data["content"] = content
	}
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func (f *scanFixture) metas(t *testing.T) []model.SessionMeta {
	t.Helper()
	p := New(f.root, pathutil.NewGitResolver())
	result, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("full scan: %v", err)
	}
	return result.Changed
}

func TestScanBasicFixture(t *testing.T) {
	f := newScanFixture(t)
	f.project("proj1", "/repo")
	f.project("proj-global-worktree", "/")
	f.session("s-user", nil)
	f.message("s-user", "user", 1, `{"text":"first question"}`)
	f.message("s-user", "assistant", 2, messageData("answer", 2))
	f.message("s-user", "assistant", 3, messageData("more", 1))
	f.message("s-user", "compaction", 4, `{"reason":"limit"}`)
	f.message("s-user", "system", 5, `{"text":"meta"}`)
	f.message("s-user", "synthetic", 6, `{"text":"notice"}`)
	f.message("s-user", "idle", 7, `{}`)
	f.session("s-empty", map[string]any{"title": nil})
	f.session("s-null-model", map[string]any{"model": nil, "title": nil})
	f.message("s-null-model", "user", 1, `{"text":"what?"}`)
	f.session("s-archived", map[string]any{"time_archived": int64(1_700_000_200_000)})
	f.session("s-child", map[string]any{"parent_id": "s-user"})
	f.message("s-child", "user", 1, `{"text":"child asks"}`)
	f.session("s-global", map[string]any{"project_id": "proj-global-worktree"})
	f.session("s-missing-cwd", map[string]any{"directory": "/does/not/exist"})

	got := f.metas(t)
	if len(got) != 7 {
		t.Fatalf("scan returned %d sessions, want 7: %+v", len(got), got)
	}
	byID := make(map[string]model.SessionMeta, len(got))
	for _, meta := range got {
		byID[meta.Ref.ID] = meta
		if meta.Ref.Agent != model.AgentOpenCode {
			t.Errorf("%s: agent = %q", meta.Ref.ID, meta.Ref.Agent)
		}
		if meta.SourcePath != filepath.Join(f.root, dbName) {
			t.Errorf("%s: source path = %q", meta.Ref.ID, meta.SourcePath)
		}
		if meta.Title == "" {
			t.Errorf("%s: empty title", meta.Ref.ID)
		}
	}
	sUser := byID["s-user"]
	if sUser.Counts != (model.MessageCounts{User: 1, Assistant: 2, ToolCalls: 3}) {
		t.Errorf("s-user counts = %+v", sUser.Counts)
	}
	if sUser.Title != "Session s-user" {
		t.Errorf("s-user title = %q", sUser.Title)
	}
	if sUser.FirstPrompt != "first question" {
		t.Errorf("s-user first prompt = %q", sUser.FirstPrompt)
	}
	if sUser.Model != "anthropic/claude-sonnet-4" || sUser.AgentName != "build" ||
		sUser.AgentVersion != "2.0.18" || sUser.CostUSD != 0 {
		t.Errorf("s-user metadata = %+v", sUser)
	}
	if sUser.CreatedAt.IsZero() || sUser.UpdatedAt.IsZero() ||
		!sUser.CreatedAt.Equal(timeFromMs(1_700_000_000_000)) {
		t.Errorf("s-user times = %v, %v", sUser.CreatedAt, sUser.UpdatedAt)
	}
	if sUser.RepoRoot != "/repo" {
		t.Errorf("s-user repo root = %q", sUser.RepoRoot)
	}
	sEmpty := byID["s-empty"]
	if sEmpty.Counts != (model.MessageCounts{}) {
		t.Errorf("s-empty counts = %+v", sEmpty.Counts)
	}
	if sEmpty.Title != "(untitled)" {
		t.Errorf("s-empty title = %q, want (untitled)", sEmpty.Title)
	}
	if sNull := byID["s-null-model"]; sNull.Model != "" || sNull.Title != "what?" || sNull.Counts.User != 1 {
		t.Errorf("s-null-model = %+v", sNull)
	}
	if !byID["s-archived"].Archived {
		t.Errorf("s-archived not marked archived")
	}
	if byID["s-user"].Archived {
		t.Errorf("s-user marked archived")
	}
	if sChild := byID["s-child"]; sChild.ParentID != "s-user" {
		t.Errorf("s-child parent = %q", sChild.ParentID)
	}
	if sGlobal := byID["s-global"]; sGlobal.RepoRoot != "" {
		t.Errorf("s-global repo root = %q, want empty (worktree /)", sGlobal.RepoRoot)
	}
	if sMissing := byID["s-missing-cwd"]; !sMissing.CWDMissing {
		t.Errorf("s-missing-cwd not marked CWDMissing")
	}
}

func timeFromMs(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

func TestScanV2Golden(t *testing.T) {
	f := newScanFixture(t)
	f.project("proj", "/source/main")
	f.project("global", "/")
	f.session("alpha", map[string]any{
		"project_id": "proj", "directory": "/source/worktree", "title": nil,
		"cost": 1.25, "tokens_input": 101, "tokens_output": 50,
		"tokens_reasoning": 15, "tokens_cache_read": 20, "tokens_cache_write": 5,
		"time_created": int64(1_700_000_000_000), "time_updated": int64(1_700_000_100_000),
	})
	f.message("alpha", "user", 1, `{"text":"  Please   fix\n this issue.  ","files":[{"data":"BASE64_SHOULD_NOT_APPEAR"}]}`)
	f.message("alpha", "assistant", 2, messageData("answer", 2))
	f.message("alpha", "user", 3, `{"text":"follow-up"}`)
	f.message("alpha", "assistant", 4, messageData("more", 1))
	f.message("alpha", "assistant", 5, messageData("done", 1))
	f.session("child", map[string]any{
		"project_id": "global", "parent_id": "alpha", "directory": "",
		"model": nil, "title": nil, "time_archived": int64(1_700_000_200_000),
	})
	f.session("empty", map[string]any{
		"project_id": "proj", "directory": "/source/worktree", "title": nil,
	})
	metas := f.metas(t)
	for i := range metas {
		metas[i].SourcePath = "<fixture>/opencode.db"
	}
	golden.JSON(t, "scan_v2", metas)
}

func TestScanFirstPromptPreferenceAndTruncation(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", map[string]any{"title": "Custom Title"})
	long := strings.Repeat("word ", 200)
	f.message("s1", "user", 1, `{"text":"`+long+`"}`)
	f.message("s1", "user", 2, `{"text":"second"}`)
	got := f.metas(t)
	if len(got) != 1 {
		t.Fatalf("scan returned %d sessions", len(got))
	}
	if got[0].Title != "Custom Title" {
		t.Errorf("nonempty row title should win: %q", got[0].Title)
	}
	if got[0].FirstPrompt != model.TruncateRunes(model.OneLine(long), 300) {
		t.Errorf("first prompt = %q", got[0].FirstPrompt)
	}
	if strings.Contains(got[0].FirstPrompt, "\n") {
		t.Errorf("first prompt is multiline: %q", got[0].FirstPrompt)
	}
	// 300-rune cap
	if n := len([]rune(got[0].FirstPrompt)); n > 300 {
		t.Errorf("first prompt has %d runes", n)
	}
}

func TestScanTitleFallbackToLatestUserPrompt(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", map[string]any{"title": nil})
	f.message("s1", "user", 1, `{"text":"the opening prompt"}`)
	f.message("s1", "user", 2, `{"text":"the latest prompt"}`)
	f.message("s1", "user", 3, `{"text":"  \n "}`)
	f.message("s1", "user", 4, `{"files":[{"name":"a.png"}]}`)
	got := f.metas(t)
	if len(got) != 1 || got[0].Title != "the latest prompt" || got[0].FirstPrompt != "the opening prompt" {
		t.Fatalf("title fallback = %+v", got)
	}
}

func TestScanInvalidJSONAndUnknownType(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", nil)
	f.message("s1", "user", 1, "{invalid")
	f.message("s1", "assistant", 2, messageData("ok", 1))
	f.message("s1", "assistant", 3, "{invalid")
	f.message("s1", "weird-type", 4, `{}`)
	result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Changed) != 1 {
		t.Fatalf("scan returned %d sessions", len(result.Changed))
	}
	if result.Changed[0].Counts != (model.MessageCounts{User: 1, Assistant: 2, ToolCalls: 1}) {
		t.Errorf("counts = %+v; invalid JSON must not abort roles/tools", result.Changed[0].Counts)
	}
	if result.Changed[0].FirstPrompt != "" {
		t.Errorf("first prompt from invalid JSON = %q", result.Changed[0].FirstPrompt)
	}
	if result.Diag.ParseErrors != 2 || result.Diag.UnknownTypes["weird-type"] != 1 || len(result.Diag.Warnings) != 2 {
		t.Errorf("diagnostics = %+v", result.Diag)
	}
}

func TestScanIncrementalCursor(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", nil)
	f.message("s1", "user", 1, `{"text":"hello"}`)
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(first.Changed) != 1 || first.State.Cursor == "" {
		t.Fatalf("first scan = %+v", first)
	}
	if first.State.Cursor != "1700000100000" {
		t.Fatalf("cursor = %q, want max time_updated", first.State.Cursor)
	}
	// No changes: unchanged IDs must be preserved, not removed.
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(second.Changed) != 0 || len(second.Removed) != 0 {
		t.Fatalf("second scan = %+v", second)
	}
	// Update s1 and insert s2 at a later millisecond.
	if _, err := f.db.Exec(`UPDATE session_v2 SET time_updated=? WHERE id='s1'`, 1_700_000_200_000); err != nil {
		t.Fatal(err)
	}
	f.session("s2", map[string]any{"time_updated": int64(1_700_000_300_000)})
	f.message("s2", "user", 1, `{"text":"new"}`)
	third, err := p.Scan(t.Context(), second.State)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if len(third.Changed) != 2 || len(third.Removed) != 0 {
		t.Fatalf("third scan changed=%d removed=%d", len(third.Changed), len(third.Removed))
	}
	if third.State.Cursor != "1700000300000" {
		t.Fatalf("third cursor = %q", third.State.Cursor)
	}
	// Equal-ms boundary must be revisited by the >= query but not re-reported
	// when the metadata is unchanged.
	fourth, err := p.Scan(t.Context(), third.State)
	if err != nil {
		t.Fatalf("fourth scan: %v", err)
	}
	if len(fourth.Changed) != 0 {
		t.Fatalf("boundary rescan changed = %d", len(fourth.Changed))
	}
}

func TestScanRemoval(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", nil)
	f.session("s2", nil)
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(first.Changed) != 2 {
		t.Fatalf("first scan changed = %d", len(first.Changed))
	}
	if _, err := f.db.Exec(`DELETE FROM session_v2 WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(second.Removed) != 1 || second.Removed[0].ID != "s1" {
		t.Fatalf("second scan removed = %+v", second.Removed)
	}
	// Deleting s1 must not have dropped s1's messages or s2's checkpoint.
	if len(second.Changed) != 0 {
		t.Fatalf("removal also reported changed = %+v", second.Changed)
	}
}

func TestScanNullAssistantData(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", nil)
	f.message("s1", "user", 1, `{"text":"hi"}`)
	// Insert one row with SQL NULL data, and one with malformed text.
	if _, err := f.db.Exec(`INSERT INTO session_message (session_id, type, seq, time_created, data)
		VALUES (?, ?, ?, ?, NULL)`, "s1", "assistant", 2, int64(1_700_000_050_000)); err != nil {
		t.Fatal(err)
	}
	f.message("s1", "assistant", 3, "not json")
	p := New(f.root, nil)
	result, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Changed) != 1 {
		t.Fatalf("scan returned %d sessions", len(result.Changed))
	}
	// NULL or invalid assistant data must be counted as a message but not as a
	// tool call, and the batch must not abort.
	if got := result.Changed[0].Counts; got.User != 1 || got.Assistant != 2 || got.ToolCalls != 0 {
		t.Errorf("counts = %+v", got)
	}
	if result.Diag.ParseErrors != 2 || len(result.Diag.Warnings) != 2 {
		t.Errorf("diagnostics = %+v", result.Diag)
	}
}

func TestScanNonFiniteCost(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", map[string]any{"cost": math.Inf(1)})
	p := New(f.root, nil)
	result, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(result.Changed) != 1 {
		t.Fatalf("scan returned %d sessions", len(result.Changed))
	}
	if result.Changed[0].CostUSD != 0 {
		t.Errorf("cost = %v, want 0 for a non-finite value", result.Changed[0].CostUSD)
	}
	if result.Diag.ParseErrors != 0 || len(result.Diag.Warnings) != 1 {
		t.Errorf("diagnostics = %+v", result.Diag)
	}
}

func TestScanSameMillisecondInsertion(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", map[string]any{"time_updated": int64(1_700_000_000_000)})
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	// A row inserted with a timestamp equal to the cursor (e.g. restored data)
	// is new and must appear, not be silently skipped by a > query.
	f.session("s2", map[string]any{"time_updated": int64(1_700_000_000_000)})
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(second.Changed) != 1 || second.Changed[0].Ref.ID != "s2" {
		t.Fatalf("same-ms insertion changed = %+v", second.Changed)
	}
}

func TestScanBackdatedInsertion(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", map[string]any{"time_updated": int64(1_700_000_100_000)})
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	// Inserted below the cursor: the ID sweep must fetch its metadata.
	f.session("s-old", map[string]any{"time_updated": int64(1_699_000_000_000)})
	f.message("s-old", "user", 1, `{"text":"old prompt"}`)
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(second.Changed) != 1 || second.Changed[0].Ref.ID != "s-old" {
		t.Fatalf("backdated insertion changed = %+v", second.Changed)
	}
	if second.Changed[0].FirstPrompt != "old prompt" {
		t.Fatalf("backdated meta = %+v", second.Changed[0])
	}
	// Cursor stays at the maximum, not at the backdated row's timestamp.
	if second.State.Cursor != "1700000100000" {
		t.Fatalf("cursor regressed to %q", second.State.Cursor)
	}
}

func TestScanIncrementalPreservesUnchangedMeta(t *testing.T) {
	f := newScanFixture(t)
	f.project("proj1", "/repo")
	f.session("s1", map[string]any{"directory": "/repo/sub"})
	f.message("s1", "user", 1, `{"text":"hello"}`)
	f.message("s1", "assistant", 2, messageData("answer", 3))
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	// Touch time_created only below the cursor; the incremental query must
	// skip the row, and the checkpoint keeps the old metadata.
	if _, err := f.db.Exec(`UPDATE session_v2 SET time_updated=?, time_created=? WHERE id='s1'`, 1_700_000_000_000, 1_690_000_000_000); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(second.Changed) != 0 || len(second.Removed) != 0 {
		t.Fatalf("unchanged scan emitted %+v", second)
	}
}

func TestScanRejectsCorruptCheckpointWithoutStrandingRemoval(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", nil)
	f.session("s2", nil)
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if _, err := f.db.Exec(`DELETE FROM session_v2 WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	// A lost ID set cannot be rebuilt from the current DB: s1's deletion would
	// become invisible. Reject the state rather than silently replacing it.
	broken := first.State
	broken.Sources = make(map[string]provider.SourceState, len(first.State.Sources))
	for path, source := range first.State.Sources {
		broken.Sources[path] = source
	}
	path := filepath.Join(f.root, dbName)
	broken.Sources[path] = provider.SourceState{Checkpoint: json.RawMessage("{oops")}
	second, err := p.Scan(t.Context(), broken)
	if err == nil || !strings.Contains(err.Error(), "invalid v2 checkpoint") {
		t.Fatalf("broken checkpoint scan = %+v, %v", second, err)
	}
	if len(second.Changed) != 0 || len(second.Removed) != 0 || len(second.State.Sources) != 0 {
		t.Fatalf("broken checkpoint published a partial scan: %+v", second)
	}
	// The original state still has the deleted ID; a retry with it reconciles.
	recovered, err := p.Scan(t.Context(), first.State)
	if err != nil || len(recovered.Removed) != 1 || recovered.Removed[0].ID != "s1" {
		t.Fatalf("retry with intact state = %+v, %v", recovered, err)
	}
}

// TestV2ReadSnapshot mutates the WAL-backed fixture after the ID sweep and
// before the metadata/count queries. Each query on the read transaction must
// agree on the old snapshot, while a new scan sees the committed mutation.
func TestV2ReadSnapshot(t *testing.T) {
	f := newScanFixture(t)
	if _, err := f.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	f.session("s1", nil)
	f.message("s1", "user", 1, `{"text":"before"}`)
	p := New(f.root, nil)
	reader, err := sqliteread.Open(t.Context(), p.dbPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	tx, err := reader.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := v2IDs(t.Context(), tx)
	if err != nil || !reflect.DeepEqual(ids, []string{"s1"}) {
		t.Fatalf("snapshot ID sweep = %v, %v", ids, err)
	}
	// A separate writer can commit while the reader holds a WAL snapshot.
	if _, err := f.db.Exec(`DELETE FROM session_v2 WHERE id='s1'`); err != nil {
		t.Fatal(err)
	}
	f.session("s2", nil)
	f.message("s1", "assistant", 2, messageData("late", 1))
	var diag provider.Diagnostics
	metas, err := p.v2Metas(t.Context(), tx, true, 0, true, nil, &diag)
	if err != nil {
		t.Fatalf("metadata in snapshot: %v", err)
	}
	if len(metas) != 1 || metas[0].Ref.ID != "s1" || metas[0].Counts != (model.MessageCounts{User: 1}) || metas[0].FirstPrompt != "before" {
		t.Fatalf("metadata mixed snapshots: %+v", metas)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Ref.ID != "s2" {
		t.Fatalf("fresh scan = %+v, %v", result, err)
	}
}

func TestScanAbsentAndSchemaEdgeCases(t *testing.T) {
	t.Run("absent database with no previous state", func(t *testing.T) {
		p := New(filepath.Join(t.TempDir(), "no-such-root"), nil)
		result, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("absent scan: %v", err)
		}
		if len(result.Changed) != 0 || len(result.Removed) != 0 {
			t.Errorf("absent scan = %+v", result)
		}
	})
	t.Run("absent database retains previous state via error", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("s1", nil)
		p := New(f.root, nil)
		first, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("first scan: %v", err)
		}
		_ = f.db.Close() // Windows cannot remove an open file.
		if err := os.Remove(filepath.Join(f.root, dbName)); err != nil {
			t.Fatal(err)
		}
		_, err = p.Scan(t.Context(), first.State)
		if err == nil || !strings.Contains(err.Error(), "disappeared") {
			t.Fatalf("removed DB error = %v", err)
		}
	})
	t.Run("dropped message schema retains previous sessions", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("s1", nil)
		p := New(f.root, nil)
		first, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("first scan: %v", err)
		}
		if _, err := f.db.Exec(`DROP TABLE session_message`); err != nil {
			t.Fatal(err)
		}
		result, err := p.Scan(t.Context(), first.State)
		if err == nil || !strings.Contains(err.Error(), "lost its v2 schema") {
			t.Fatalf("dropped schema scan = %+v, %v", result, err)
		}
		if len(result.Changed) != 0 || len(result.Removed) != 0 || len(result.State.Sources) != 0 {
			t.Fatalf("dropped schema published a partial scan: %+v", result)
		}
	})
	t.Run("replaced message schema retains previous sessions", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("s1", nil)
		p := New(f.root, nil)
		first, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("first scan: %v", err)
		}
		if _, err := f.db.Exec(`DROP TABLE session_message; CREATE TABLE session_message (id INTEGER PRIMARY KEY);`); err != nil {
			t.Fatal(err)
		}
		result, err := p.Scan(t.Context(), first.State)
		if err == nil || !strings.Contains(err.Error(), "v2 role counts") {
			t.Fatalf("replaced schema scan = %+v, %v", result, err)
		}
		if len(result.Changed) != 0 || len(result.Removed) != 0 || len(result.State.Sources) != 0 {
			t.Fatalf("replaced schema published a partial scan: %+v", result)
		}
	})
	t.Run("v1-only store scans", func(t *testing.T) {
		f := newScanFixture(t)
		if _, err := f.db.Exec(`DROP TABLE session_v2; DROP TABLE session_message`); err != nil {
			t.Fatal(err)
		}
		f.v1Session("v1-only", nil)
		f.v1Message("m1", "v1-only", "user", 1_700_000_000_001)
		f.v1Part("p1", "m1", "v1-only", 1_700_000_000_002, `{"type":"text","text":"hello"}`)
		result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("v1 store scan: %v", err)
		}
		if len(result.Changed) != 1 || result.Changed[0].Ref.ID != "v1-only" || result.Changed[0].Counts.User != 1 {
			t.Errorf("v1 store scan = %+v", result)
		}
	})
	t.Run("v1 store replaced by a v2 store", func(t *testing.T) {
		// OpenCode 2 starts a new database: the 1.x sessions are gone and
		// must not hide the new ones behind a retention error.
		f := newScanFixture(t)
		f.v1Session("old", nil)
		f.v1Message("m1", "old", "user", 1_700_000_000_001)
		f.v1Part("p1", "m1", "old", 1_700_000_000_002, `{"type":"text","text":"hello"}`)
		p := New(f.root, nil)
		first, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil || len(first.Changed) != 1 {
			t.Fatalf("v1 scan = %+v, %v", first, err)
		}
		if _, err := f.db.Exec(`DROP TABLE part; DROP TABLE message; DROP TABLE session`); err != nil {
			t.Fatal(err)
		}
		f.session("new", nil)
		result, err := p.Scan(t.Context(), first.State)
		if err != nil {
			t.Fatalf("v2 scan: %v", err)
		}
		if len(result.Changed) != 1 || result.Changed[0].Ref.ID != "new" {
			t.Errorf("changed = %+v", result.Changed)
		}
		if len(result.Removed) != 1 || result.Removed[0].ID != "old" {
			t.Errorf("removed = %+v", result.Removed)
		}
	})
	t.Run("incomplete v2 schema warns", func(t *testing.T) {
		root := t.TempDir()
		fixtureDB(t, root, "session_v2")
		p := New(root, nil)
		result, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("partial scan: %v", err)
		}
		if len(result.Changed) != 0 || len(result.Diag.Warnings) == 0 {
			t.Errorf("partial scan = %+v", result)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("s1", nil)
		p := New(f.root, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := p.Scan(ctx, provider.ScanState{})
		if err == nil {
			t.Fatalf("canceled scan: %v", err)
		}
	})
	t.Run("cursor without checkpoint is rejected", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("s1", nil)
		p := New(f.root, nil)
		first, err := p.Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatalf("first scan: %v", err)
		}
		cursorOnly := provider.ScanState{Cursor: first.State.Cursor}
		_, err = p.Scan(t.Context(), cursorOnly)
		if err == nil || !strings.Contains(err.Error(), "cursor without v2 checkpoint") {
			t.Fatalf("cursor-only state error = %v", err)
		}
	})
}

func TestScanSortOrder(t *testing.T) {
	f := newScanFixture(t)
	for _, id := range []string{"c", "a", "b"} {
		f.session(id, nil)
	}
	got := f.metas(t)
	var keys []string
	for _, meta := range got {
		keys = append(keys, meta.Ref.Key())
	}
	if want := []string{"opencode:a", "opencode:b", "opencode:c"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("sort order = %v, want %v", keys, want)
	}
}

func TestScanLargeBatchSplitting(t *testing.T) {
	f := newScanFixture(t)
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("s-%03d", i)
		f.session(id, nil)
		f.message(id, "user", 1, `{"text":"prompt `+id+`"}`)
	}
	got := f.metas(t)
	if len(got) != 250 {
		t.Fatalf("scan returned %d sessions, want 250", len(got))
	}
	for _, meta := range got {
		if meta.Counts.User != 1 || meta.Counts.ToolCalls != 0 {
			t.Errorf("%s counts = %+v", meta.Ref.ID, meta.Counts)
		}
	}
}

func TestParseModelName(t *testing.T) {
	cases := []struct {
		name, raw, want string
		warn            bool
	}{
		{name: "null SQL", raw: "", want: ""},
		{name: "null JSON", raw: "null", want: ""},
		{name: "empty object", raw: "{}", want: ""},
		{name: "provider and id", raw: `{"providerID":"openai","id":"gpt-5"}`, want: "openai/gpt-5"},
		{name: "id only", raw: `{"id":"gpt-5"}`, want: "gpt-5"},
		{name: "provider only", raw: `{"providerID":"openai"}`, want: "openai"},
		{name: "variant ignored", raw: `{"providerID":"openai","id":"gpt-5","variant":"fast"}`, want: "openai/gpt-5"},
		{name: "not json", raw: `garbage`, warn: true},
		{name: "json array", raw: `[1,2]`, warn: true},
		{name: "bad id type", raw: `{"id":4}`, want: "", warn: true},
		{name: "bad id with good provider", raw: `{"providerID":"openai","id":4}`, want: "openai", warn: true},
		{name: "bad provider with good id", raw: `{"providerID":4,"id":"gpt-5"}`, want: "", warn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := parseModelName(sql.NullString{String: tc.raw, Valid: tc.raw != ""})
			if got != tc.want || warn != tc.warn {
				t.Errorf("parseModelName(%q) = %q, %v; want %q, %v", tc.raw, got, warn, tc.want, tc.warn)
			}
		})
	}
}
