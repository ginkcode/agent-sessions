package index

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var (
	errCorruptDB      = errors.New("database integrity check failed")
	errNewerVersionDB = errors.New("recorded migration version is newer than bundled migrations")
)

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var list []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid migration filename: %s", entry.Name())
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid migration version in %s: %w", entry.Name(), err)
		}
		content, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		list = append(list, migration{
			version: ver,
			name:    entry.Name(),
			sql:     string(content),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].version < list[j].version
	})
	return list, nil
}

func checkIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check(1)")
	if err != nil {
		return fmt.Errorf("check database integrity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("check database integrity: %w", err)
		}
		return errCorruptDB
	}
	var res string
	if err := rows.Scan(&res); err != nil {
		return fmt.Errorf("read database integrity result: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("%w: %s", errCorruptDB, res)
	}
	return rows.Err()
}

func runMigrations(ctx context.Context, db *sql.DB, migrations []migration) error {
	if err := checkIntegrity(ctx, db); err != nil {
		return err
	}

	// Ensure schema_migrations table exists.
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[int]bool)
	var maxApplied int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
		if v > maxApplied {
			maxApplied = v
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema_migrations: %w", err)
	}

	var maxBundled int
	for _, m := range migrations {
		if m.version > maxBundled {
			maxBundled = m.version
		}
	}

	if maxApplied > maxBundled {
		return fmt.Errorf("%w: recorded version %d > max bundled %d", errNewerVersionDB, maxApplied, maxBundled)
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("apply migration %s (v%d): %w", m.name, m.version, err)
		}
	}

	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := applyMigrationTx(ctx, tx, m); err != nil {
		return err
	}
	return tx.Commit()
}

func applyMigrationTx(ctx context.Context, tx *sql.Tx, m migration) error {
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("exec migration sql: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)
		ON CONFLICT(version) DO UPDATE SET applied_at = excluded.applied_at;
	`, m.version, nowMs); err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	return nil
}

// resetSchema empties db in place: it drops every table and view, of this
// schema or any other, and applies migrations, all in one write
// transaction. Readers in this or any other process see either the old
// database or the new empty one, and the file is never removed or
// replaced, which Windows refuses while anyone has it open.
func resetSchema(ctx context.Context, db *sql.DB, migrations []migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// With foreign keys on, DROP TABLE first deletes every row and runs
	// the cascades. The pragma is a no-op inside a transaction, so it is
	// switched on this connection only, and back before the pool reuses it.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON"); err != nil {
			// Never hand a connection without foreign keys back to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reset tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Virtual tables go first: dropping one drops its shadow tables, which
	// cannot be dropped on their own before it.
	vtabs, err := schemaObjects(ctx, tx,
		`SELECT type, name FROM sqlite_schema WHERE type = 'table' AND sql LIKE 'CREATE VIRTUAL TABLE%'`)
	if err != nil {
		return err
	}
	if err := dropObjects(ctx, tx, vtabs); err != nil {
		return err
	}
	// Indexes and triggers go with their tables.
	rest, err := schemaObjects(ctx, tx,
		`SELECT type, name FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		return err
	}
	if err := dropObjects(ctx, tx, rest); err != nil {
		return err
	}
	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_schema WHERE name = 'sqlite_sequence'`).Scan(&seq); err != nil {
		return fmt.Errorf("probe sqlite_sequence: %w", err)
	}
	if seq > 0 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sqlite_sequence"); err != nil {
			return fmt.Errorf("clear sqlite_sequence: %w", err)
		}
	}

	for _, m := range migrations {
		if err := applyMigrationTx(ctx, tx, m); err != nil {
			return fmt.Errorf("apply migration %s (v%d): %w", m.name, m.version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset tx: %w", err)
	}
	return nil
}

type schemaObject struct{ typ, name string }

// schemaObjects reads the whole result before anything is dropped.
func schemaObjects(ctx context.Context, tx *sql.Tx, query string) ([]schemaObject, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list schema: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []schemaObject
	for rows.Next() {
		var o schemaObject
		if err := rows.Scan(&o.typ, &o.name); err != nil {
			return nil, fmt.Errorf("scan schema: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list schema: %w", err)
	}
	return out, nil
}

func dropObjects(ctx context.Context, tx *sql.Tx, objs []schemaObject) error {
	for _, o := range objs {
		kind := "TABLE"
		if o.typ == "view" {
			kind = "VIEW"
		}
		quoted := `"` + strings.ReplaceAll(o.name, `"`, `""`) + `"`
		if _, err := tx.ExecContext(ctx, "DROP "+kind+" IF EXISTS "+quoted); err != nil {
			return fmt.Errorf("drop %s %s: %w", o.typ, o.name, err)
		}
	}
	return nil
}
