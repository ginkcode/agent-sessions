// Package index manages the private SQLite database caching session metadata,
// scanner state, and FTS indexes.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// DB manages the private cache SQLite database.
// It coordinates single-writer access to prevent SQLITE_BUSY contention.
type DB struct {
	db       *sql.DB
	path     string
	cacheDir string
	dsn      string
	writeMu  sync.Mutex
}

// Open opens or initializes the cache database in cacheDir.
// If cacheDir is empty, the default application cache directory is used.
// The cache directory is enforced to 0700, the database file to 0600,
// and symlinks are strictly rejected.
func Open(ctx context.Context, cacheDir string) (*DB, error) {
	if cacheDir == "" {
		roots, err := paths.Default()
		if err != nil {
			return nil, fmt.Errorf("index: resolve default cache dir: %w", err)
		}
		cacheDir = roots.Cache
	}

	absDir, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("index: invalid cache dir %q: %w", cacheDir, err)
	}
	cacheDir = filepath.Clean(absDir)

	// Enforce 0700 permissions and reject symlinks for cache directory.
	fi, err := os.Lstat(cacheDir)
	switch {
	case err == nil:
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("index: cache directory %q is a symlink", cacheDir)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("index: cache directory %q is not a directory", cacheDir)
		}
		if err := os.Chmod(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("index: chmod cache dir %q: %w", cacheDir, err)
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("index: mkdir cache dir %q: %w", cacheDir, err)
		}
		fi, err := os.Lstat(cacheDir)
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("index: cache directory %q is a symlink", cacheDir)
		}
	default:
		return nil, fmt.Errorf("index: stat cache dir %q: %w", cacheDir, err)
	}

	dbPath := filepath.Join(cacheDir, "index.db")
	fi, err = os.Lstat(dbPath)
	switch {
	case err == nil:
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("index: database %q is a symlink", dbPath)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("index: database %q is not a regular file", dbPath)
		}
		if err := os.Chmod(dbPath, 0o600); err != nil {
			return nil, fmt.Errorf("index: chmod database file %q: %w", dbPath, err)
		}
	case os.IsNotExist(err):
		f, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("index: create database file %q: %w", dbPath, err)
		}
		_ = f.Close()
	default:
		return nil, fmt.Errorf("index: stat database file %q: %w", dbPath, err)
	}

	for _, sidecar := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if sfi, err := os.Lstat(sidecar); err == nil {
			if sfi.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("index: sidecar %q is a symlink", sidecar)
			}
			_ = os.Chmod(sidecar, 0o600)
		}
	}

	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(ON)")
	uri := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(dbPath),
		RawQuery: params.Encode(),
	}
	dsn := uri.String()

	restoreUmask := setPrivateUmask()
	defer restoreUmask()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("index: open sqlite database %q: %w", dbPath, err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("index: ping database %q: %w", dbPath, err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("index: load migrations: %w", err)
	}

	err = runMigrations(ctx, sqlDB, migrations)
	if err != nil {
		// Rebuild clean private database on integrity error or newer migration version.
		if errors.Is(err, errCorruptDB) || errors.Is(err, errNewerVersionDB) {
			_ = sqlDB.Close()
			if wipeErr := wipeDBFiles(dbPath); wipeErr != nil {
				return nil, fmt.Errorf("index: wipe corrupt/newer db: %w", wipeErr)
			}
			f, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o600)
			if err != nil {
				return nil, fmt.Errorf("index: recreate database file: %w", err)
			}
			_ = f.Close()

			sqlDB, err = sql.Open("sqlite", dsn)
			if err != nil {
				return nil, fmt.Errorf("index: reopen rebuilt database: %w", err)
			}
			sqlDB.SetMaxOpenConns(2)
			sqlDB.SetMaxIdleConns(2)
			if err := sqlDB.PingContext(ctx); err != nil {
				_ = sqlDB.Close()
				return nil, fmt.Errorf("index: ping rebuilt database: %w", err)
			}
			if err := runMigrations(ctx, sqlDB, migrations); err != nil {
				_ = sqlDB.Close()
				return nil, fmt.Errorf("index: run migrations on rebuilt database: %w", err)
			}
		} else {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("index: run migrations: %w", err)
		}
	}

	// Enforce 0600 permissions on sidecars if created during opening/migrations.
	for _, sidecar := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Lstat(sidecar); err == nil {
			_ = os.Chmod(sidecar, 0o600)
		}
	}

	return &DB{
		db:       sqlDB,
		path:     dbPath,
		cacheDir: cacheDir,
		dsn:      dsn,
	}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if d.db == nil {
		return nil
	}
	err := d.db.Close()
	d.db = nil
	return err
}

// Path returns the path to the SQLite index database file.
func (d *DB) Path() string {
	return d.path
}

// CacheDir returns the path to the cache directory.
func (d *DB) CacheDir() string {
	return d.cacheDir
}

// SQLDB returns the underlying database handle for query execution.
func (d *DB) SQLDB() *sql.DB {
	return d.db
}

// Rebuild tears down the existing database and sidecars, reopens a clean file,
// and applies all embedded migrations from scratch.
func (d *DB) Rebuild(ctx context.Context) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	restoreUmask := setPrivateUmask()
	defer restoreUmask()

	if d.db != nil {
		_ = d.db.Close()
		d.db = nil
	}

	if err := wipeDBFiles(d.path); err != nil {
		return fmt.Errorf("index: wipe db files: %w", err)
	}

	f, err := os.OpenFile(d.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("index: recreate db file: %w", err)
	}
	_ = f.Close()

	sqlDB, err := sql.Open("sqlite", d.dsn)
	if err != nil {
		return fmt.Errorf("index: reopen rebuilt db: %w", err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("index: ping rebuilt db: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("index: load migrations: %w", err)
	}

	if err := runMigrations(ctx, sqlDB, migrations); err != nil {
		_ = sqlDB.Close()
		return fmt.Errorf("index: run migrations: %w", err)
	}

	d.db = sqlDB

	for _, sidecar := range []string{d.path + "-wal", d.path + "-shm"} {
		if _, err := os.Lstat(sidecar); err == nil {
			_ = os.Chmod(sidecar, 0o600)
		}
	}

	return nil
}

// LoadCatalog returns all stored sessions from the cache, ordered newest first.
// Volatile liveness fields (Live, LiveStatus) are cleared.
func (d *DB) LoadCatalog(ctx context.Context) ([]model.SessionMeta, error) {
	if d.db == nil {
		return nil, errors.New("index: database closed")
	}

	rows, err := d.db.QueryContext(ctx, "SELECT meta_json FROM sessions ORDER BY updated_at DESC")
	if err != nil {
		return nil, fmt.Errorf("index: query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	metas := make([]model.SessionMeta, 0)
	for rows.Next() {
		var metaJSON string
		if err := rows.Scan(&metaJSON); err != nil {
			return nil, fmt.Errorf("index: scan session row: %w", err)
		}
		var m model.SessionMeta
		if err := json.Unmarshal([]byte(metaJSON), &m); err != nil {
			return nil, fmt.Errorf("index: unmarshal session meta: %w", err)
		}
		m.Live = false
		m.LiveStatus = ""
		metas = append(metas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: iterate sessions: %w", err)
	}
	return metas, nil
}

// LoadState returns the persisted scan state for the given agent.
// If no state has been saved yet, an empty ScanState with an initialized Sources map is returned.
func (d *DB) LoadState(ctx context.Context, agent model.AgentID) (provider.ScanState, error) {
	if d.db == nil {
		return provider.ScanState{}, errors.New("index: database closed")
	}

	var stateJSON string
	err := d.db.QueryRowContext(ctx, "SELECT state_json FROM provider_state WHERE agent = ?", string(agent)).Scan(&stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return provider.ScanState{
			Sources: make(map[string]provider.SourceState),
		}, nil
	}
	if err != nil {
		return provider.ScanState{}, fmt.Errorf("index: query provider state for %s: %w", agent, err)
	}

	var state provider.ScanState
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return provider.ScanState{}, fmt.Errorf("index: unmarshal provider state for %s: %w", agent, err)
	}
	if state.Sources == nil {
		state.Sources = make(map[string]provider.SourceState)
	}
	return state, nil
}

// CommitScan writes Changed and Removed sessions and updates the agent's ScanState
// atomically in a single transaction. It increments fts_revision for changed sessions
// and enqueues indexing jobs in fts_jobs.
func (d *DB) CommitScan(ctx context.Context, agent model.AgentID, result provider.ScanResult) error {
	if agent == "" {
		return errors.New("index: agent ID is required")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if d.db == nil {
		return errors.New("index: database closed")
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("index: begin commit tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const upsertSessionSQL = `
INSERT INTO sessions (
    ref, agent, id, parent_id, source_path, meta_json,
    title, cwd, repo_root, created_at, updated_at,
    msg_total, tokens, cost, archived, model,
    fts_revision, fts_indexed_revision
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0)
ON CONFLICT(ref) DO UPDATE SET
    parent_id = excluded.parent_id,
    source_path = excluded.source_path,
    meta_json = excluded.meta_json,
    title = excluded.title,
    cwd = excluded.cwd,
    repo_root = excluded.repo_root,
    created_at = excluded.created_at,
    updated_at = excluded.updated_at,
    msg_total = excluded.msg_total,
    tokens = excluded.tokens,
    cost = excluded.cost,
    archived = excluded.archived,
    model = excluded.model,
    fts_revision = sessions.fts_revision + 1;
`

	const upsertJobSQL = `
INSERT INTO fts_jobs (ref, revision, updated_at, attempts, last_error)
SELECT ref, fts_revision, ?, 0, '' FROM sessions WHERE ref = ?
ON CONFLICT(ref) DO UPDATE SET
    revision = excluded.revision,
    updated_at = excluded.updated_at,
    attempts = 0,
    last_error = '';
`

	nowMs := time.Now().UTC().UnixMilli()

	for _, m := range result.Changed {
		cleanMeta := m
		cleanMeta.Live = false
		cleanMeta.LiveStatus = ""
		metaBytes, err := json.Marshal(cleanMeta)
		if err != nil {
			return fmt.Errorf("index: marshal meta for %s: %w", m.Ref.Key(), err)
		}

		var createdAtMs, updatedAtMs int64
		if !m.CreatedAt.IsZero() {
			createdAtMs = m.CreatedAt.UTC().UnixMilli()
		}
		if !m.UpdatedAt.IsZero() {
			updatedAtMs = m.UpdatedAt.UTC().UnixMilli()
		}

		totalTokens := m.Tokens.Input + m.Tokens.Output + m.Tokens.Reasoning + m.Tokens.CacheRead + m.Tokens.CacheWrite
		archived := 0
		if m.Archived {
			archived = 1
		}

		refKey := m.Ref.Key()
		agentStr := string(m.Ref.Agent)
		if agentStr == "" {
			agentStr = string(agent)
			refKey = agentStr + ":" + m.Ref.ID
		}

		_, err = tx.ExecContext(ctx, upsertSessionSQL,
			refKey,
			agentStr,
			m.Ref.ID,
			m.ParentID,
			m.SourcePath,
			string(metaBytes),
			m.Title,
			m.CWD,
			m.RepoRoot,
			createdAtMs,
			updatedAtMs,
			m.Counts.Total(),
			totalTokens,
			m.CostUSD,
			archived,
			m.Model,
		)
		if err != nil {
			return fmt.Errorf("index: upsert session %s: %w", refKey, err)
		}

		_, err = tx.ExecContext(ctx, upsertJobSQL, nowMs, refKey)
		if err != nil {
			return fmt.Errorf("index: upsert fts job for %s: %w", refKey, err)
		}
	}

	for _, rem := range result.Removed {
		agentStr := string(rem.Agent)
		if agentStr == "" {
			agentStr = string(agent)
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE agent = ? AND id = ?", agentStr, rem.ID)
		if err != nil {
			return fmt.Errorf("index: delete removed session %s:%s: %w", agentStr, rem.ID, err)
		}
	}

	stateBytes, err := json.Marshal(result.State)
	if err != nil {
		return fmt.Errorf("index: marshal state for %s: %w", agent, err)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO provider_state (agent, state_json) VALUES (?, ?)
ON CONFLICT(agent) DO UPDATE SET state_json = excluded.state_json;
`, string(agent), string(stateBytes))
	if err != nil {
		return fmt.Errorf("index: upsert provider state for %s: %w", agent, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: commit scan tx: %w", err)
	}

	return nil
}

func wipeDBFiles(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %q: %w", path, err)
		}
	}
	return nil
}
