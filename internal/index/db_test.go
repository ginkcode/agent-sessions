package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestEmptyDBMigration(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify schema_migrations table exists and has version 1
	var version int
	err = db.SQLDB().QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version = 1").Scan(&version)
	if err != nil {
		t.Fatalf("QueryRow schema_migrations failed: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1, got %d", version)
	}

	// Verify key tables exist
	tables := []string{"sessions", "provider_state", "fts_jobs", "fts_docs", "fts_messages"}
	for _, tbl := range tables {
		var exists int
		err := db.SQLDB().QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?", tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if exists == 0 {
			t.Errorf("expected table %s to exist", tbl)
		}
	}
}

func TestReopenIdempotence(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	m := model.SessionMeta{
		Ref: model.SessionRef{
			Agent: model.AgentClaude,
			ID:    "test-sess-1",
		},
		Title:      "Test Session 1",
		SourcePath: "/tmp/test.jsonl",
		CWD:        "/home/user/project",
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now,
		Counts:     model.MessageCounts{User: 2, Assistant: 2},
		Tokens:     model.TokenUsage{Input: 100, Output: 200},
		CostUSD:    0.05,
		Archived:   false,
		Live:       true, // volatile
		LiveStatus: "busy",
		Model:      "claude-sonnet-5",
	}

	state := provider.ScanState{
		Sources: map[string]provider.SourceState{
			"/tmp/test.jsonl": {
				Size:      1024,
				ModTimeNs: 123456789,
				Offset:    1024,
			},
		},
		Cursor: "cursor-123",
	}

	res := provider.ScanResult{
		Changed: []model.SessionMeta{m},
		State:   state,
	}

	if err := db.CommitScan(ctx, model.AgentClaude, res); err != nil {
		t.Fatalf("CommitScan failed: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen the database
	reopened, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Reopen failed: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	// Verify catalog
	catalog, err := reopened.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(catalog) != 1 {
		t.Fatalf("expected 1 session, got %d", len(catalog))
	}
	gotMeta := catalog[0]
	if gotMeta.Ref.Key() != "claude-code:test-sess-1" {
		t.Errorf("expected ref claude-code:test-sess-1, got %s", gotMeta.Ref.Key())
	}
	if gotMeta.Title != "Test Session 1" {
		t.Errorf("expected title 'Test Session 1', got %s", gotMeta.Title)
	}
	// Verify volatile flags are cleared
	if gotMeta.Live || gotMeta.LiveStatus != "" {
		t.Errorf("expected Live/LiveStatus cleared, got live=%v status=%q", gotMeta.Live, gotMeta.LiveStatus)
	}

	// Verify state
	gotState, err := reopened.LoadState(ctx, model.AgentClaude)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if gotState.Cursor != "cursor-123" {
		t.Errorf("expected cursor 'cursor-123', got %s", gotState.Cursor)
	}
	if src, ok := gotState.Sources["/tmp/test.jsonl"]; !ok || src.Size != 1024 {
		t.Errorf("expected source with size 1024, got %+v", src)
	}
}

func TestNewerVersionRebuild(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Commit initial session
	m := model.SessionMeta{
		Ref:   model.SessionRef{Agent: model.AgentClaude, ID: "sess-v1"},
		Title: "Session V1",
	}
	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{m},
	}); err != nil {
		t.Fatalf("CommitScan failed: %v", err)
	}
	_ = db.Close()

	// Manually corrupt version in DB by inserting higher version 999
	dbFile := filepath.Join(tmpDir, "index.db")
	rawDB, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	_, err = rawDB.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (999, 1234567)")
	if err != nil {
		t.Fatalf("insert higher version: %v", err)
	}
	_ = rawDB.Close()

	// Reopen with index.Open — it should detect version 999 > bundled 1 and rebuild
	rebuilt, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open after newer version failed: %v", err)
	}
	defer func() { _ = rebuilt.Close() }()

	// Verify DB was wiped and rebuilt fresh
	catalog, err := rebuilt.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog on rebuilt DB: %v", err)
	}
	if len(catalog) != 0 {
		t.Errorf("expected empty catalog after rebuild, got %d sessions", len(catalog))
	}

	var ver int
	err = rebuilt.SQLDB().QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&ver)
	if err != nil {
		t.Fatalf("query version: %v", err)
	}
	if ver != 1 {
		t.Errorf("expected version 1 on rebuilt DB, got %d", ver)
	}
}

func TestRollbackOnInjectedWriteError(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Initial commit
	initialState := provider.ScanState{Cursor: "initial-cursor"}
	err = db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{
			{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "sess-stable"}, Title: "Stable"},
		},
		State: initialState,
	})
	if err != nil {
		t.Fatalf("initial commit failed: %v", err)
	}

	// Try committing with a canceled context
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel() // cancel immediately

	badRes := provider.ScanResult{
		Changed: []model.SessionMeta{
			{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "sess-should-fail"}, Title: "Fail"},
		},
		State: provider.ScanState{Cursor: "failed-cursor"},
	}

	err = db.CommitScan(canceledCtx, model.AgentClaude, badRes)
	if err == nil {
		t.Fatal("expected error with canceled context, got nil")
	}

	// Verify catalog and state remain unchanged
	catalog, err := db.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(catalog) != 1 || catalog[0].Ref.ID != "sess-stable" {
		t.Errorf("expected only sess-stable in catalog, got %+v", catalog)
	}

	state, err := db.LoadState(ctx, model.AgentClaude)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if state.Cursor != "initial-cursor" {
		t.Errorf("expected initial-cursor, got %s", state.Cursor)
	}
}

func TestCascadeFTSDelete(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Commit a session
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "sess-fts-1"}
	err = db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{
			{Ref: ref, Title: "Quantum Computing Discussion"},
		},
	})
	if err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	// Verify fts_jobs row exists
	var jobCount int
	err = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_jobs WHERE ref = ?", ref.Key()).Scan(&jobCount)
	if err != nil || jobCount != 1 {
		t.Fatalf("expected 1 fts_job, got %d (err: %v)", jobCount, err)
	}

	// Manually insert into fts_docs (which triggers fts_messages insert)
	_, err = db.SQLDB().ExecContext(ctx, `
		INSERT INTO fts_docs (ref, message_index, kind, body)
		VALUES (?, 0, 'text', 'Superposition and quantum entanglement explained.')
	`, ref.Key())
	if err != nil {
		t.Fatalf("insert fts_docs: %v", err)
	}

	// Query FTS5 table
	var matchCount int
	err = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'entanglement'").Scan(&matchCount)
	if err != nil || matchCount != 1 {
		t.Fatalf("expected 1 fts match for 'entanglement', got %d (err: %v)", matchCount, err)
	}

	// Remove session via CommitScan
	err = db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Removed: []model.SessionRef{ref},
	})
	if err != nil {
		t.Fatalf("CommitScan remove: %v", err)
	}

	// Verify session removed
	var sessCount int
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE ref = ?", ref.Key()).Scan(&sessCount)
	if sessCount != 0 {
		t.Errorf("expected session removed from sessions table, got count %d", sessCount)
	}

	// Verify fts_jobs cascaded
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_jobs WHERE ref = ?", ref.Key()).Scan(&jobCount)
	if jobCount != 0 {
		t.Errorf("expected fts_jobs cascade deletion, got count %d", jobCount)
	}

	// Verify fts_docs cascaded
	var docCount int
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_docs WHERE ref = ?", ref.Key()).Scan(&docCount)
	if docCount != 0 {
		t.Errorf("expected fts_docs cascade deletion, got count %d", docCount)
	}

	// Verify fts_messages cleaned up via trigger
	_ = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'entanglement'").Scan(&matchCount)
	if matchCount != 0 {
		t.Errorf("expected fts_messages to have 0 matches after cascade delete, got %d", matchCount)
	}
}

func TestPermissionsAndPragmas(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	cacheDir := filepath.Join(tmpDir, "agent-cache")
	db, err := Open(ctx, cacheDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Check cache dir permissions: 0700
	dirInfo, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("expected cacheDir mode 0700, got %04o", perm)
	}

	// Check index.db permissions: 0600
	dbInfo, err := os.Stat(db.Path())
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if perm := dbInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected index.db mode 0600, got %04o", perm)
	}

	// Trigger a write so WAL/SHM exist
	err = db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{
			{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "perm-test"}, Title: "Perms"},
		},
	})
	if err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	// Check WAL permissions if present
	walPath := db.Path() + "-wal"
	if walInfo, err := os.Stat(walPath); err == nil {
		if perm := walInfo.Mode().Perm(); perm != 0o600 {
			t.Errorf("expected WAL mode 0600, got %04o", perm)
		}
	}

	// Check PRAGMAs
	var journalMode string
	if err := db.SQLDB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode 'wal', got %q", journalMode)
	}

	var foreignKeys int
	if err := db.SQLDB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("expected foreign_keys 1, got %d", foreignKeys)
	}

	var busyTimeout int
	if err := db.SQLDB().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("expected busy_timeout 5000, got %d", busyTimeout)
	}

	_ = db.Close()

	// Test Symlink Rejection for cacheDir
	symCacheDir := filepath.Join(tmpDir, "sym-cache")
	if err := os.Symlink(cacheDir, symCacheDir); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	_, err = Open(ctx, symCacheDir)
	if err == nil {
		t.Errorf("expected Open on symlink cache dir to fail, but succeeded")
	}

	// Test Symlink Rejection for index.db
	targetDir := filepath.Join(tmpDir, "target-cache")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	targetDB := filepath.Join(targetDir, "index.db")
	realDB := filepath.Join(cacheDir, "index.db")
	if err := os.Symlink(realDB, targetDB); err != nil {
		t.Fatalf("symlink db: %v", err)
	}
	_, err = Open(ctx, targetDir)
	if err == nil {
		t.Errorf("expected Open on symlink index.db to fail, but succeeded")
	}
}

func TestClearLiveFlagsRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "live-test"}
	m := model.SessionMeta{
		Ref:        ref,
		Title:      "Live Session",
		Live:       true,
		LiveStatus: "busy",
	}

	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{Changed: []model.SessionMeta{m}}); err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	// Check stored JSON in database
	var metaJSON string
	err = db.SQLDB().QueryRowContext(ctx, "SELECT meta_json FROM sessions WHERE ref = ?", ref.Key()).Scan(&metaJSON)
	if err != nil {
		t.Fatalf("query meta_json: %v", err)
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(metaJSON), &rawMap); err != nil {
		t.Fatalf("unmarshal stored json: %v", err)
	}
	if live, ok := rawMap["live"]; ok && live != false {
		t.Errorf("expected live: false in stored JSON, got %v", live)
	}
	if status, ok := rawMap["liveStatus"]; ok && status != "" {
		t.Errorf("expected empty liveStatus in stored JSON, got %v", status)
	}

	// Check LoadCatalog
	catalog, err := db.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(catalog) != 1 {
		t.Fatalf("expected 1 catalog item, got %d", len(catalog))
	}
	if catalog[0].Live || catalog[0].LiveStatus != "" {
		t.Errorf("expected cleared Live/LiveStatus, got live=%v status=%q", catalog[0].Live, catalog[0].LiveStatus)
	}
}

func TestUnicodeAndPathDSN(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Path with spaces, hashes, unicode characters
	unicodeDir := filepath.Join(tmpDir, "câché #1 with spáces & ünicode")
	db, err := Open(ctx, unicodeDir)
	if err != nil {
		t.Fatalf("Open with unicode/special path failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentCodex, ID: "unicode-1"}
	err = db.CommitScan(ctx, model.AgentCodex, provider.ScanResult{
		Changed: []model.SessionMeta{
			{Ref: ref, Title: "Testing ñoño and #tag with spaces"},
		},
	})
	if err != nil {
		t.Fatalf("CommitScan failed: %v", err)
	}

	catalog, err := db.LoadCatalog(ctx)
	if err != nil {
		t.Fatalf("LoadCatalog failed: %v", err)
	}
	if len(catalog) != 1 || catalog[0].Title != "Testing ñoño and #tag with spaces" {
		t.Errorf("unexpected catalog result: %+v", catalog)
	}
}

func TestCompiledFTS5Smoke(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	db, err := Open(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "fts-smoke-1"}
	err = db.CommitScan(ctx, model.AgentOpenCode, provider.ScanResult{
		Changed: []model.SessionMeta{
			{Ref: ref, Title: "Refactoring Database Layer"},
		},
	})
	if err != nil {
		t.Fatalf("CommitScan: %v", err)
	}

	// Insert title row (message_index = -1) and message rows
	docs := []struct {
		msgIdx int
		kind   string
		body   string
	}{
		{-1, "title", "Refactoring Database Layer"},
		{0, "text", "How do we implement SQLite FTS5 full text search?"},
		{1, "reasoning", "Thinking through inverted index algorithms and ranking."},
		{2, "tool", "grep -rn 'FTS5' internal/"},
	}

	for _, d := range docs {
		_, err := db.SQLDB().ExecContext(ctx,
			"INSERT INTO fts_docs (ref, message_index, kind, body) VALUES (?, ?, ?, ?)",
			ref.Key(), d.msgIdx, d.kind, d.body)
		if err != nil {
			t.Fatalf("insert fts_docs %+v: %v", d, err)
		}
	}

	// Test MATCH queries
	testQueries := []struct {
		query        string
		expectedHits int
	}{
		{"Refactoring", 1},
		{"SQLite", 1},
		{"FTS5", 2}, // appears in text and tool
		{"algorithms", 1},
		{"nonexistentword123", 0},
	}

	for _, tq := range testQueries {
		var cnt int
		err := db.SQLDB().QueryRowContext(ctx,
			fmt.Sprintf("SELECT count(*) FROM fts_messages WHERE fts_messages MATCH '%s'", tq.query)).Scan(&cnt)
		if err != nil {
			t.Fatalf("query MATCH %s failed: %v", tq.query, err)
		}
		if cnt != tq.expectedHits {
			t.Errorf("query %s expected %d hits, got %d", tq.query, tq.expectedHits, cnt)
		}
	}

	// Test Update trigger
	_, err = db.SQLDB().ExecContext(ctx,
		"UPDATE fts_docs SET body = 'Updated text with blockchain keyword' WHERE ref = ? AND message_index = 0",
		ref.Key())
	if err != nil {
		t.Fatalf("update fts_docs: %v", err)
	}

	var updatedHit int
	err = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'blockchain'").Scan(&updatedHit)
	if err != nil || updatedHit != 1 {
		t.Errorf("expected 1 hit for updated word 'blockchain', got %d (err: %v)", updatedHit, err)
	}

	// Old word from updated doc should no longer match (except if in other docs)
	var oldHit int
	err = db.SQLDB().QueryRowContext(ctx, "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'implement'").Scan(&oldHit)
	if err != nil || oldHit != 0 {
		t.Errorf("expected 0 hits for replaced word 'implement', got %d (err: %v)", oldHit, err)
	}
}
