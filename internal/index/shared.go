package index

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/filelock"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Several processes can use one cache dir at once: desktop apps and remote
// servers, possibly of different app versions. The rules that keep them from
// breaking each other:
//
//   - The database is named by schema version and by a key of the provider
//     roots it indexes (index-v<N>-<key>.db). A version never opens,
//     migrates or wipes another schema's file, and processes that scan
//     different roots never share an index.
//   - Per file, the process holding its lock (index-v<N>-<key>.lock) is the
//     only writer. Everyone else of that schema and key opens the file
//     read-only.
//   - A writer never deletes or replaces its own file, which readers in
//     other processes may have open (and Windows then refuses): Rebuild
//     and a newer schema reset it in place in one transaction. Only a
//     file too damaged for that is deleted.
//   - Another file is deleted only by a writer that can take that file's
//     lock too, and only after it has been unused for a while.

// legacyFileName is the database of builds before schema-named files.
const legacyFileName = "index.db"

// UnusedFor is how long another database must go unused before a
// writer deletes it. It is long enough that switching between two app
// versions now and then never forces a rebuild.
const UnusedFor = 30 * 24 * time.Hour

var schemaVersion = sync.OnceValue(func() int {
	list, err := loadMigrations()
	if err != nil {
		return 0
	}
	v := 0
	for _, m := range list {
		v = max(v, m.version)
	}
	return v
})

// SchemaVersion is the newest bundled migration.
func SchemaVersion() int { return schemaVersion() }

// RootsKey names the index of one set of provider roots: processes that
// scan the same roots share it.
func RootsKey(roots ...string) string {
	h := sha256.New()
	for _, r := range roots {
		if r != "" {
			r = filepath.Clean(r)
		}
		h.Write([]byte(r))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// FileName is the database file for a schema version and roots key. An
// empty key, used by tests and tools with no roots, gives index-v<N>.db.
func FileName(schema int, key string) string { return fileBase(schema, key) + ".db" }

func lockFileName(schema int, key string) string { return fileBase(schema, key) + ".lock" }

func fileBase(schema int, key string) string {
	if key == "" {
		return fmt.Sprintf("index-v%d", schema)
	}
	return fmt.Sprintf("index-v%d-%s", schema, key)
}

// ErrLocked means another process is the writer of this database.
var ErrLocked = filelock.ErrLocked

// ErrNoIndex means the database file does not exist yet.
var ErrNoIndex = errors.New("index: database does not exist yet")

var errReadOnly = errors.New("index: database is open read-only")

// Lock makes its holder the writer of one database in a cache dir.
type Lock struct{ l *filelock.Lock }

// TryLock takes the writer lock for this build's schema and key in cacheDir
// without waiting. ErrLocked means another process is the writer; open the
// database with OpenReadOnly instead.
func TryLock(cacheDir, key string) (*Lock, error) {
	dir, err := prepareCacheDir(cacheDir)
	if err != nil {
		return nil, err
	}
	return tryLockFile(dir, SchemaVersion(), key)
}

func tryLockFile(dir string, schema int, key string) (*Lock, error) {
	l, err := filelock.TryLock(filepath.Join(dir, lockFileName(schema, key)))
	if err != nil {
		return nil, err
	}
	return &Lock{l: l}, nil
}

// Unlock releases the writer role. Close the database first.
func (l *Lock) Unlock() error {
	if l == nil {
		return nil
	}
	return l.l.Unlock()
}

// OpenReadOnly opens this build's database for key in cacheDir without writing to
// it: no migrations, no integrity repair, query_only. It fails with
// ErrNoIndex before a writer has created the file, and until the writer has
// migrated it to this schema.
func OpenReadOnly(ctx context.Context, cacheDir, key string) (*DB, error) {
	dir, err := prepareCacheDir(cacheDir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, FileName(SchemaVersion(), key))
	fi, err := os.Lstat(dbPath)
	switch {
	case os.IsNotExist(err):
		return nil, ErrNoIndex
	case err != nil:
		return nil, fmt.Errorf("index: stat database %q: %w", dbPath, err)
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("index: database %q is a symlink", dbPath)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("index: database %q is not a regular file", dbPath)
	}
	// On Windows a FileInfo from Lstat looks its file ID up by path only at
	// the first os.SameFile. Do it now, so Replaced compares against the
	// file opened here rather than whatever is at the path later.
	_ = os.SameFile(fi, fi)

	sqlDB, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("index: open %q read-only: %w", dbPath, err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)
	applied, err := appliedVersion(ctx, sqlDB)
	if err == nil && applied != SchemaVersion() {
		err = fmt.Errorf("schema %d, want %d", applied, SchemaVersion())
	}
	if err == nil {
		// The file must still be the one fi describes, or Replaced would
		// never notice that this handle reads a stale copy.
		if now, statErr := os.Lstat(dbPath); statErr != nil || !os.SameFile(fi, now) {
			err = errors.New("replaced while opening")
		}
	}
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("%w: %w", ErrNoIndex, err)
	}
	return &DB{db: sqlDB, path: dbPath, cacheDir: dir, readOnly: true, file: fi}, nil
}

func readOnlyDSN(path string) string {
	return roDSN(path, true)
}

// roDSN opens path read-only. queryOnly also refuses VACUUM INTO, which a
// seed copy needs.
func roDSN(path string, queryOnly bool) string {
	params := url.Values{}
	params.Add("mode", "ro")
	params.Add("_pragma", "busy_timeout(5000)")
	if queryOnly {
		params.Add("_pragma", "query_only(1)")
	}
	return pathutil.SQLiteURI(path, params)
}

// appliedVersion is the newest migration recorded in db, 0 for none.
func appliedVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Replaced reports whether the file this read-only handle opened is no
// longer the one at its path, because a writer had to delete a damaged
// file or it was cleaned up. The handle then reads a stale copy and should
// be reopened. Rebuild resets the file in place and never replaces it.
func (d *DB) Replaced() bool {
	if !d.readOnly || d.file == nil {
		return false
	}
	fi, err := os.Stat(d.path)
	return err != nil || !os.SameFile(fi, d.file)
}

// LoadSnapshot reads the catalog and every provider's scan state in one read
// transaction, so the states describe exactly the sessions returned. A
// reader seeds its in-memory scanner from it: sessions changed after the
// snapshot differ from the states and are rescanned.
func (d *DB) LoadSnapshot(ctx context.Context) ([]model.SessionMeta, map[model.AgentID]provider.ScanState, error) {
	if d.db == nil {
		return nil, nil, errors.New("index: database closed")
	}
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("index: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	metas, err := loadCatalog(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT agent, state_json FROM provider_state")
	if err != nil {
		return nil, nil, fmt.Errorf("index: query provider states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	states := make(map[model.AgentID]provider.ScanState)
	for rows.Next() {
		var agent, stateJSON string
		if err := rows.Scan(&agent, &stateJSON); err != nil {
			return nil, nil, fmt.Errorf("index: scan provider state: %w", err)
		}
		st, err := decodeState(stateJSON)
		if err != nil {
			return nil, nil, fmt.Errorf("index: provider state for %s: %w", agent, err)
		}
		states[model.AgentID(agent)] = st
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("index: iterate provider states: %w", err)
	}
	return metas, states, nil
}

// seedFrom fills a missing dbPath with a copy of an older schema's database
// for the same key, or the legacy index.db, so Open migrates it forward instead of rebuilding
// the whole index. VACUUM INTO reads the source in one transaction, so the
// copy is consistent even while that version's writer is running. A newer
// schema is never used: migrations only go forward.
func seedFrom(ctx context.Context, dir, dbPath string, schema int, key string) {
	for _, src := range seedCandidates(dir, schema, key) {
		if vacuumInto(ctx, src, dbPath, schema) == nil {
			return
		}
	}
}

func seedCandidates(dir string, schema int, key string) []string {
	type cand struct {
		path   string
		schema int
	}
	var list []cand
	for _, name := range listDir(dir) {
		if s, k, ok := parseIndexFile(name); ok && s < schema && k == key {
			list = append(list, cand{filepath.Join(dir, name), s})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].schema > list[j].schema })
	out := make([]string, 0, len(list)+1)
	for _, c := range list {
		out = append(out, c.path)
	}
	return append(out, filepath.Join(dir, legacyFileName))
}

func vacuumInto(ctx context.Context, src, dst string, schema int) error {
	fi, err := os.Lstat(src)
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	srcDB, err := sql.Open("sqlite", roDSN(src, false))
	if err != nil {
		return err
	}
	defer func() { _ = srcDB.Close() }()
	applied, err := appliedVersion(ctx, srcDB)
	if err != nil {
		return err
	}
	if applied == 0 || applied > schema {
		return fmt.Errorf("source schema %d cannot seed %d", applied, schema)
	}

	restore := setPrivateUmask()
	defer restore()
	tmp := fmt.Sprintf("%s.seed-%d", dst, os.Getpid())
	_ = os.Remove(tmp)
	if _, err := srcDB.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Cleanup deletes the databases of other schemas and keys that no process
// has used for unusedFor. A file is only touched while this call holds its
// lock, so a running writer's file is never removed; a legacy index.db has
// no lock, so only its age protects it. Lock files stay (see package
// filelock). The caller must be the writer of its own schema and key.
func Cleanup(cacheDir, key string, unusedFor time.Duration) {
	dir, err := prepareCacheDir(cacheDir)
	if err != nil {
		return
	}
	own := SchemaVersion()
	now := time.Now()
	for _, name := range listDir(dir) {
		path := filepath.Join(dir, name)
		if name == legacyFileName {
			if now.Sub(lastUsed(path)) > unusedFor {
				_ = wipeDBFiles(path)
			}
			continue
		}
		schema, k, ok := parseIndexFile(name)
		if !ok || (schema == own && k == key) {
			continue
		}
		l, err := tryLockFile(dir, schema, k)
		if err != nil {
			continue
		}
		if now.Sub(lastUsed(path)) > unusedFor {
			_ = wipeDBFiles(path)
		}
		_ = l.Unlock()
	}
}

// lastUsed is the newest mtime of a database and its WAL. A writer touches
// its database on open, so this also covers a process that only read.
func lastUsed(dbPath string) time.Time {
	var t time.Time
	for _, p := range []string{dbPath, dbPath + "-wal"} {
		if fi, err := os.Lstat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	return names
}

// parseIndexFile matches "index-v<N>.db" and "index-v<N>-<key>.db", the key
// being lowercase hex.
func parseIndexFile(name string) (schema int, key string, ok bool) {
	rest, ok := strings.CutPrefix(name, "index-v")
	if !ok {
		return 0, "", false
	}
	rest, ok = strings.CutSuffix(rest, ".db")
	if !ok {
		return 0, "", false
	}
	num, key, _ := strings.Cut(rest, "-")
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 || strconv.Itoa(n) != num {
		return 0, "", false
	}
	if key != "" && strings.Trim(key, "0123456789abcdef") != "" {
		return 0, "", false
	}
	if strings.HasSuffix(rest, "-") {
		return 0, "", false
	}
	return n, key, true
}
