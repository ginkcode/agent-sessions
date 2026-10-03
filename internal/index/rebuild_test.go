package index

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
)

// requireForeignKeys checks every connection of db's pool (two) enforces
// foreign keys.
func requireForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	c1, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c1.Close() }()
	c2, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	for i, c := range []*sql.Conn{c1, c2} {
		if n := countRows(t, c, "PRAGMA foreign_keys"); n != 1 {
			t.Errorf("connection %d: foreign_keys = %d", i, n)
		}
	}
}

func claudeRef(id string) string {
	return model.SessionRef{Agent: model.AgentClaude, ID: id}.Key()
}

func countRows(t *testing.T, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any,
) int {
	t.Helper()
	var n int
	if err := q.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// Rebuild resets the file in place: the writer's handle and the file stay
// the same, a reader's open snapshot is untouched, and later reads see the
// empty, fully migrated index, FTS included.
func TestRebuildInPlaceWithOpenReader(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	w, err := Open(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	commitOne(t, w, "s1")
	commitOne(t, w, "s2")
	if _, err := w.SQLDB().ExecContext(ctx,
		`INSERT INTO fts_docs (ref, message_index, body) VALUES (?, 0, 'needle')`, claudeRef("s1")); err != nil {
		t.Fatal(err)
	}

	r, err := OpenReadOnly(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	snap, err := r.SQLDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Rollback() }()
	if n := countRows(t, snap, "SELECT count(*) FROM sessions"); n != 2 {
		t.Fatalf("snapshot before rebuild: %d sessions", n)
	}

	before, err := os.Stat(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	handle := w.SQLDB()
	if err := w.Rebuild(ctx); err != nil {
		t.Fatalf("Rebuild with a reader open: %v", err)
	}
	if w.SQLDB() != handle {
		t.Error("Rebuild replaced the writer's handle")
	}
	after, err := os.Stat(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("Rebuild replaced the file")
	}
	if r.Replaced() {
		t.Error("Replaced = true after an in-place rebuild")
	}

	if n := countRows(t, snap, "SELECT count(*) FROM sessions"); n != 2 {
		t.Errorf("open snapshot changed by the rebuild: %d sessions", n)
	}
	_ = snap.Rollback()
	if n := countRows(t, r.SQLDB(), "SELECT count(*) FROM sessions"); n != 0 {
		t.Errorf("reader after rebuild: %d sessions", n)
	}
	if v, err := appliedVersion(ctx, r.SQLDB()); err != nil || v != SchemaVersion() {
		t.Errorf("reader schema after rebuild = %d, %v", v, err)
	}
	if n := countRows(t, r.SQLDB(), "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'needle'"); n != 0 {
		t.Errorf("FTS kept %d hits through the rebuild", n)
	}
	if n := countRows(t, w.SQLDB(), "SELECT count(*) FROM sqlite_schema WHERE name LIKE 'fts_messages_%'"); n == 0 {
		t.Error("FTS shadow tables missing after rebuild")
	}

	// The rebuilt schema works, foreign keys and FTS triggers included.
	commitOne(t, w, "s3")
	if _, err := w.SQLDB().ExecContext(ctx,
		`INSERT INTO fts_docs (ref, message_index, body) VALUES (?, 0, 'needle')`, claudeRef("s3")); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, r.SQLDB(), "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'needle'"); n != 1 {
		t.Errorf("FTS after reindex: %d hits", n)
	}
	if _, err := w.SQLDB().ExecContext(ctx,
		`INSERT INTO fts_docs (ref, message_index, body) VALUES (?, 0, 'x')`, claudeRef("missing")); err == nil {
		t.Error("foreign keys off after rebuild")
	}
	requireForeignKeys(t, w.SQLDB())
}

// A reset whose migrations fail rolls back completely: the catalog and FTS
// index survive, the handle and file stay, and foreign keys are back on.
func TestResetSchemaRollsBackOnFailedMigration(t *testing.T) {
	ctx := t.Context()
	w, err := Open(ctx, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	commitOne(t, w, "s1")
	if _, err := w.SQLDB().ExecContext(ctx,
		`INSERT INTO fts_docs (ref, message_index, body) VALUES (?, 0, 'needle')`, claudeRef("s1")); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	handle := w.SQLDB()

	good, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	bad := append(append([]migration(nil), good...), migration{version: SchemaVersion() + 1, name: "bad.sql", sql: "CREATE TABLE broken ("})
	if err := resetSchema(ctx, w.SQLDB(), bad); err == nil {
		t.Fatal("reset with invalid migration SQL succeeded")
	}

	if w.SQLDB() != handle {
		t.Error("failed reset replaced the handle")
	}
	if after, err := os.Stat(w.Path()); err != nil || !os.SameFile(before, after) {
		t.Errorf("failed reset replaced the file: %v", err)
	}
	if metas, err := w.LoadCatalog(ctx); err != nil || len(metas) != 1 || metas[0].Ref.ID != "s1" {
		t.Errorf("catalog after failed reset = %+v, %v", metas, err)
	}
	if n := countRows(t, w.SQLDB(), "SELECT count(*) FROM fts_messages WHERE fts_messages MATCH 'needle'"); n != 1 {
		t.Errorf("FTS after failed reset: %d hits", n)
	}
	if v, err := appliedVersion(ctx, w.SQLDB()); err != nil || v != SchemaVersion() {
		t.Errorf("schema after failed reset = %d, %v", v, err)
	}
	requireForeignKeys(t, w.SQLDB())
	commitOne(t, w, "s2")
	if metas, err := w.LoadCatalog(ctx); err != nil || len(metas) != 2 {
		t.Errorf("writer after failed reset: %d sessions, %v", len(metas), err)
	}
}

// A reset that fails because Open was canceled leaves the file alone:
// cancellation is never taken for damage that warrants deleting it.
func TestStartOverKeepsFileWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	commitOne(t, db, "kept")
	path := db.Path()
	if _, err := db.SQLDB().ExecContext(t.Context(),
		"INSERT INTO schema_migrations (version, applied_at) VALUES (999, 1)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	dsn := pathutil.SQLiteURI(path, url.Values{"_pragma": {"busy_timeout(5000)"}})
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := startOver(ctx, sqlDB, path, dsn, migrations); !errors.Is(err, context.Canceled) {
		if got != nil {
			_ = got.Close()
		}
		t.Fatalf("startOver canceled: err = %v, want context.Canceled", err)
	}

	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("canceled reset replaced the file: %v", err)
	}
	raw, err := sql.Open("sqlite", roDSN(path, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if n := countRows(t, raw, "SELECT count(*) FROM sessions"); n != 1 {
		t.Errorf("canceled reset lost sessions: %d left", n)
	}
	if v, err := appliedVersion(t.Context(), raw); err != nil || v != 999 {
		t.Errorf("canceled reset changed the schema: %d, %v", v, err)
	}
}

// A file whose schema a newer build wrote is reset in place, even while
// another process has it open read-only, which Windows forbids deleting.
func TestOpenResetsNewerSchemaWithOutsideReader(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := Open(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	commitOne(t, db, "old")
	path := db.Path()
	if _, err := db.SQLDB().ExecContext(ctx, `
INSERT INTO schema_migrations (version, applied_at) VALUES (999, 1);
CREATE TABLE future_only (x INTEGER REFERENCES sessions(ref));
CREATE VIRTUAL TABLE future_fts USING fts5(body);
INSERT INTO future_fts (body) VALUES ('future');
CREATE VIEW future_view AS SELECT * FROM future_only;`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	outside, err := sql.Open("sqlite", roDSN(path, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outside.Close() }()
	if n := countRows(t, outside, "SELECT count(*) FROM sessions"); n != 1 {
		t.Fatalf("outside reader: %d sessions", n)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	rebuilt, err := Open(ctx, dir, "")
	if err != nil {
		t.Fatalf("Open with a newer schema and an outside reader: %v", err)
	}
	defer func() { _ = rebuilt.Close() }()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("Open replaced the file instead of resetting it")
	}
	if metas, err := rebuilt.LoadCatalog(ctx); err != nil || len(metas) != 0 {
		t.Errorf("catalog after reset: %d sessions, %v", len(metas), err)
	}
	if n := countRows(t, rebuilt.SQLDB(),
		"SELECT count(*) FROM sqlite_schema WHERE name LIKE 'future%'"); n != 0 {
		t.Errorf("%d objects of the newer schema survived", n)
	}
	for _, q := range []*sql.DB{rebuilt.SQLDB(), outside} {
		if v, err := appliedVersion(ctx, q); err != nil || v != SchemaVersion() {
			t.Errorf("schema after reset = %d, %v", v, err)
		}
	}
}

// Replaced compares against the file OpenReadOnly opened, even when the
// first check comes after the swap. On Windows a FileInfo resolves its file
// ID lazily, by path, unless OpenReadOnly pins it.
func TestReplacedDetectsSwapBeforeFirstCheck(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	w, err := Open(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	commitOne(t, w, "s1")
	r, err := OpenReadOnly(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// Close every handle so the swap works on Windows too, then move the
	// file aside rather than delete it, so its inode cannot be reused.
	_ = r.Close()
	_ = w.Close()
	if err := os.Rename(w.Path(), w.Path()+".old"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{w.Path() + "-wal", w.Path() + "-shm"} {
		_ = os.Remove(s)
	}
	fresh, err := Open(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if !r.Replaced() {
		t.Error("Replaced = false after the file was swapped")
	}
}
