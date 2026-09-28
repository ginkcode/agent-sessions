package sqliteread

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // Register the SQLite database/sql driver.
)

// initDB creates a fresh database and executes statements from test code, never
// the tool's DB.
func initDB(t *testing.T, statements ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatalf("initializing DB: %v", err)
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("executing %q: %v", s, err)
		}
	}
	return path
}

func TestOpenRejectsWrites(t *testing.T) {
	t.Parallel()
	path := initDB(t, "CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (42);")

	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer func() { _ = db.Close() }()

	// SQLite rejects write statements on a read-only connection.
	if _, err := db.ExecContext(ctx, "CREATE TABLE t2 (x)"); err == nil {
		t.Fatal("read-only DB accepted CREATE TABLE")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM t"); err == nil {
		t.Fatal("read-only DB accepted DELETE")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("read-only DB accepted INSERT")
	}

	// Reads still work.
	var n int
	if err := db.QueryRowContext(ctx, "SELECT x FROM t").Scan(&n); err != nil {
		t.Fatalf("select from read-only DB: %v", err)
	}
	if n != 42 {
		t.Fatalf("got %d, want 42", n)
	}
}

func TestHasTable(t *testing.T) {
	t.Parallel()
	path := initDB(t,
		"CREATE TABLE session_v2 (id TEXT)",
		"CREATE TABLE session (id TEXT)",
	)
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, tc := range []struct {
		name string
		want bool
	}{
		{"session_v2", true},
		{"session", true},
		{"threads", false},
		{"thread_spawn_edges", false},
	} {
		got, err := HasTable(ctx, db, tc.name)
		if err != nil {
			t.Fatalf("HasTable(%q): %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("HasTable(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHasTableRejectsUnallowlistedNames(t *testing.T) {
	t.Parallel()
	path := initDB(t, "CREATE TABLE evil (id TEXT)")
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, name := range []string{
		"evil",                                   // Real table, but not allowlisted.
		"sqlite_master",                          // Internal table.
		"select",                                 // Reserved SQL identifier.
		`session_v2"; DROP TABLE session_v2; --`, // SQL syntax must never enter a query.
		"credential",                             // Auth table.
		"account",                                // Auth table.
		"control_account",                        // Auth table.
		"not a real table name",                  // Nonexistent.
	} {
		if _, err := HasTable(ctx, db, name); err == nil {
			t.Errorf("HasTable(%q) accepted unallowlisted name", name)
		}
	}
}

func TestColumns(t *testing.T) {
	t.Parallel()
	path := initDB(t, `
		CREATE TABLE session_v2 (
			id TEXT PRIMARY KEY,
			title TEXT,
			time_created INTEGER NOT NULL,
			time_archived INTEGER
		)`)
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, err := Columns(ctx, db, "session_v2")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	want := map[string]bool{"id": true, "title": true, "time_created": true, "time_archived": true}
	for name := range want {
		if !got[name] {
			t.Errorf("Columns(session_v2) missing column %q; got %v", name, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("Columns(session_v2) = %v, want %v", got, want)
	}
}

func TestColumnsOfMissingTable(t *testing.T) {
	t.Parallel()
	path := initDB(t)
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, err := Columns(ctx, db, "threads")
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Columns of missing table = %v, want empty map", got)
	}
}

func TestColumnsRejectsUnallowlistedNames(t *testing.T) {
	t.Parallel()
	path := initDB(t, "CREATE TABLE evil (id TEXT)")
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, name := range []string{"evil", "credential", "sqlite_master"} {
		if _, err := Columns(ctx, db, name); err == nil {
			t.Errorf("Columns(%q) accepted unallowlisted name", name)
		}
	}
}

func TestOpenMissingFile(t *testing.T) {
	t.Parallel()
	// mode=ro causes SQLite to fail if file doesn't exist instead of creating it.
	missingPath := filepath.Join(t.TempDir(), "nonexistent.db")
	if _, err := Open(context.Background(), missingPath); err == nil {
		t.Fatal("Open of nonexistent file should fail")
	}
}

func TestOpenBadPermissions(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}
	path := initDB(t, "CREATE TABLE session_v2 (id TEXT)")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, err := Open(context.Background(), path)
	if err == nil {
		t.Fatal("opening an unreadable DB should fail")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not wrap DB path %q", err, path)
	}
}

func TestOpenUnwritableDirectorySurfacesSQLiteError(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("skipping directory permission test when running as root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.db")

	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec("PRAGMA journal_mode=WAL; CREATE TABLE session_v2 (id TEXT);"); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	// Remove -shm and -wal so SQLite must create -shm on open.
	_ = os.Remove(path + "-shm")
	_ = os.Remove(path + "-wal")

	// Make directory read-only so SQLite cannot create -shm.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	// Opening a WAL DB without -shm in a read-only dir must surface the SQLite
	// error rather than silently ignoring it.
	_, err = Open(context.Background(), path)
	if err == nil {
		t.Fatal("expected error opening WAL DB without -shm in read-only directory")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not wrap DB path %q", err, path)
	}
	if !strings.Contains(err.Error(), "-shm") {
		t.Errorf("error %q does not mention -shm guidance", err)
	}
}

func TestOpenPathWithSpaces(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "dir with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "my agent session.db")
	creator, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := creator.Exec("CREATE TABLE session_v2 (id TEXT)"); err != nil {
		_ = creator.Close()
		t.Fatal(err)
	}
	if err := creator.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open path with spaces: %v", err)
	}
	defer func() { _ = db.Close() }()

	ok, err := HasTable(ctx, db, "session_v2")
	if err != nil || !ok {
		t.Fatalf("HasTable(session_v2) = %v, %v", ok, err)
	}
}

func TestOpenCanceledContext(t *testing.T) {
	t.Parallel()
	path := initDB(t, "CREATE TABLE session_v2 (id TEXT)")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Open(ctx, path)
	if err == nil {
		t.Fatal("Open with canceled context should fail")
	}
}

func TestQueryCanceledContext(t *testing.T) {
	t.Parallel()
	path := initDB(t, "CREATE TABLE session_v2 (id TEXT)")

	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err = HasTable(canceledCtx, db, "session_v2")
	if err == nil {
		t.Fatal("HasTable with canceled context should fail")
	}

	_, err = Columns(canceledCtx, db, "session_v2")
	if err == nil {
		t.Fatal("Columns with canceled context should fail")
	}
}

func TestOpenLiveWALDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "live.db")

	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()

	if _, err := writer.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec("CREATE TABLE session_v2 (id TEXT); INSERT INTO session_v2 VALUES ('a')"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	reader, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open live WAL database: %v", err)
	}
	defer func() { _ = reader.Close() }()

	// Insert into WAL while reader is open.
	if _, err := writer.Exec("INSERT INTO session_v2 VALUES ('b')"); err != nil {
		t.Fatal(err)
	}

	// Reader must see the live uncheckpointed WAL row.
	var count int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_v2").Scan(&count); err != nil {
		t.Fatalf("querying live WAL DB: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d rows, want 2 (uncheckpointed WAL data must be visible)", count)
	}
}

func TestDSN(t *testing.T) {
	t.Parallel()
	dsnStr := dsn("/path/to/db.sqlite")
	parsed, err := url.Parse(dsnStr)
	if err != nil {
		t.Fatalf("failed to parse DSN %q: %v", dsnStr, err)
	}
	if parsed.Scheme != "file" {
		t.Errorf("scheme = %q, want file", parsed.Scheme)
	}
	q := parsed.Query()
	if got := q.Get("mode"); got != "ro" {
		t.Errorf("mode = %q, want ro", got)
	}
	if got := q.Get("_txlock"); got != "deferred" {
		t.Errorf("_txlock = %q, want deferred", got)
	}
	if got := q.Get("_pragma"); got != "busy_timeout(5000)" {
		t.Errorf("_pragma = %q, want busy_timeout(5000)", got)
	}
	if strings.Contains(dsnStr, "immutable") {
		t.Errorf("DSN %q contains immutable flag", dsnStr)
	}
}

func TestDSNPathEscaping(t *testing.T) {
	t.Parallel()
	// A path with characters that are special in a URI must survive the DSN
	// round trip; Open resolves it to the same file.
	dsnStr := dsn("/tmp/a path?with=weird&chars/db#name.sqlite")
	parsed, err := url.Parse(dsnStr)
	if err != nil {
		t.Fatalf("parse %q: %v", dsnStr, err)
	}
	if unescaped := parsed.Path; unescaped != "/tmp/a path?with=weird&chars/db#name.sqlite" {
		t.Errorf("escaped path = %q", unescaped)
	}
	if q := parsed.Query(); q.Get("mode") != "ro" {
		t.Errorf("query params disturbed by path escaping: %v", q)
	}
}
