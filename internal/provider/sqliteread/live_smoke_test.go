package sqliteread

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// TestLiveOpenCodeSmoke runs only when OPENCODE_DB is explicitly set. It
// queries the live DB read-only and never creates or mutates any tool files.
func TestLiveOpenCodeSmoke(t *testing.T) {
	path := os.Getenv("OPENCODE_DB")
	if path == "" {
		t.Skip("set OPENCODE_DB to manually smoke-test the live OpenCode DB")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open live OpenCode DB: %v", err)
	}
	defer func() { _ = db.Close() }()
	t.Logf("open live DB: %v", time.Since(start))

	for _, name := range []string{"session_v2", "session_message", "session", "message", "part", "project"} {
		ok, err := HasTable(ctx, db, name)
		if err != nil {
			t.Fatalf("HasTable(%q): %v", name, err)
		}
		t.Logf("HasTable(%q) = %v", name, ok)
	}
	for _, name := range []string{"session_v2", "session_message"} {
		cols, err := Columns(ctx, db, name)
		if err != nil {
			t.Fatalf("Columns(%q): %v", name, err)
		}
		t.Logf("Columns(%q): %d columns", name, len(cols))
	}

	// Read a row while OpenCode holds the WAL open; no SQLITE_BUSY expected.
	var n sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_v2").Scan(&n); err != nil {
		t.Fatalf("count live sessions: %v", err)
	}
	t.Logf("live session_v2 rows: %d", n.Int64)

	// Verify write operations are strictly rejected on the live DB.
	if _, err := db.ExecContext(ctx, "CREATE TABLE probe_live_test (x)"); err == nil {
		t.Fatal("live DB unexpectedly accepted a write statement")
	}
}
