package index

import (
	"context"
	"database/sql"
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

	return tx.Commit()
}
