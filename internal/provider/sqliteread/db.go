// Package sqliteread opens live agent SQLite stores without write access and
// probes only the schema tables the providers know how to consume.
package sqliteread

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite" // Register the SQLite database/sql driver.
)

// schemaTables is the complete set of table names used by the OpenCode and
// Codex providers. In particular, it excludes credential and auth tables.
var schemaTables = map[string]bool{
	"session_v2":         true,
	"session_message":    true,
	"session":            true,
	"message":            true,
	"part":               true,
	"project":            true,
	"worktree":           true,
	"threads":            true,
	"thread_spawn_edges": true,
}

// dsn returns the read-only DSN for path. mode=ro makes SQLite refuse writes
// and refuse to create a missing file. busy_timeout rides out short writer
// locks; _txlock=deferred is SQLite's default, stated so a future change
// cannot silently take an exclusive lock. immutable is deliberately not set:
// the DB and its WAL are live and immutable can omit uncheckpointed changes.
func dsn(path string) string {
	params := url.Values{
		"mode":    {"ro"},
		"_pragma": {"busy_timeout(5000)"},
		"_txlock": {"deferred"},
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: params.Encode()}
	return uri.String()
}

// Open opens an existing SQLite database read-only. The caller must Close the
// returned DB. Do not use immutable=1: it can hide uncheckpointed live WAL data.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("sqlite read-only path %q: %w", path, err)
	}

	db, err := sql.Open("sqlite", dsn(abs))
	if err != nil {
		return nil, fmt.Errorf("sqlite read-only open %q: %w", abs, err)
	}

	// A single connection is enough for the short sequential provider queries.
	// Ping now because sql.Open alone does not check the file or the WAL.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite read-only open %q: %w (for a live WAL database, make its -shm file accessible or allow SQLite to create it in the directory)", abs, err)
	}
	return db, nil
}

// HasTable reports whether a known provider table exists. The table name is a
// bound value, not SQL syntax; unknown names are rejected even so.
func HasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	if err := checkTable(name); err != nil {
		return false, err
	}
	path := dbPath(ctx, db)
	var exists bool
	err := db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("sqlite schema %q table %q: %w", path, name, err)
	}
	return exists, nil
}

// Columns reports the columns of a known provider table. A missing table has
// no columns. Quoting is safe here because the identifier must be allowlisted.
func Columns(ctx context.Context, db *sql.DB, name string) (map[string]bool, error) {
	if err := checkTable(name); err != nil {
		return nil, err
	}
	path := dbPath(ctx, db)
	rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+name+`")`)
	if err != nil {
		return nil, fmt.Errorf("sqlite schema %q columns of %q: %w", path, name, err)
	}
	defer func() { _ = rows.Close() }()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var columnName, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &columnName, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return nil, fmt.Errorf("sqlite schema %q columns of %q: %w", path, name, err)
		}
		columns[columnName] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite schema %q columns of %q: %w", path, name, err)
	}
	return columns, nil
}

func checkTable(name string) error {
	if !schemaTables[name] {
		return fmt.Errorf("sqlite schema table %q is not allowlisted", name)
	}
	return nil
}

// database/sql deliberately hides its DSN, so retrieve the main DB path only
// for error context. This reads no user data.
func dbPath(ctx context.Context, db *sql.DB) string {
	var path string
	if err := db.QueryRowContext(ctx,
		"SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		return "<unknown database>"
	}
	return path
}
