package manage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// OpenCodeWarning is the exact warning copy required by the safety model.
const OpenCodeWarning = "OpenCode will permanently delete this session and its child sessions. It will not go to Trash and cannot be restored."

// opencodeManager handles permanent deletion via the opencode CLI.
type opencodeManager struct {
	root string
	ps   *pathSafety
	exec ExecFunc
	now  NowFunc
}

func newOpenCodeManager(root string, execFn ExecFunc, now NowFunc) (*opencodeManager, error) {
	ps, err := newPathSafety(root)
	if err != nil {
		return nil, err
	}
	if execFn == nil {
		execFn = defaultExec
	}
	if now == nil {
		now = defaultNow
	}
	return &opencodeManager{root: ps.root, ps: ps, exec: execFn, now: now}, nil
}

// canTargetRoot checks whether the provider root sits directly under a
// standard XDG_DATA_HOME hierarchy (i.e. filepath.Base(root) == "opencode").
func (om *opencodeManager) canTargetRoot() bool {
	return filepath.Base(filepath.Clean(om.root)) == "opencode"
}

// dbPath returns the path to opencode.db.
func (om *opencodeManager) dbPath() string {
	return filepath.Join(om.root, "opencode.db")
}

// plan verifies the session exists, is not recent, and enumerates its
// descendant IDs through the OpenCode database (read-only).
func (om *opencodeManager) plan(ctx context.Context, m model.SessionMeta) (descendants []string, err error) {
	if m.Ref.Agent != model.AgentOpenCode {
		return nil, ErrUnsupportedAction
	}
	if !om.canTargetRoot() {
		return nil, errors.New("opencode root is non-standard; refusing to target via XDG_DATA_HOME")
	}

	dbFile := om.dbPath()
	fi, err := os.Lstat(dbFile)
	if err != nil {
		return nil, errors.New("opencode database is unavailable")
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("opencode database is not a regular file")
	}

	db, err := sqliteread.Open(ctx, dbFile)
	if err != nil {
		return nil, errors.New("cannot open opencode database read-only")
	}
	defer func() { _ = db.Close() }()

	hasV2, err := sqliteread.HasTable(ctx, db, "session_v2")
	if err != nil || !hasV2 {
		return nil, errors.New("opencode database missing session_v2 table")
	}

	var updatedMs sql.NullInt64
	err = db.QueryRowContext(ctx, "SELECT time_updated FROM session_v2 WHERE id = ?", m.Ref.ID).Scan(&updatedMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("session not found in opencode database")
		}
		return nil, errors.New("cannot query opencode database")
	}

	// Recent activity check
	if updatedMs.Valid {
		updated := time.UnixMilli(updatedMs.Int64).UTC()
		now := om.now()
		if now.Sub(updated) < recentWindow {
			return nil, fmt.Errorf("%w: session was updated within 10 minutes", ErrLive)
		}
	}

	// Find descendants recursively. The CTE is cycle-safe: a UNION (not
	// UNION ALL) dedupes revisited rows so a corrupted parent_id cycle
	// terminates, and depth is bounded by DISTINCT ids actually present.
	const query = `
WITH RECURSIVE descendants(id) AS (
    SELECT id FROM session_v2 WHERE parent_id = ?
    UNION
    SELECT s.id FROM session_v2 s JOIN descendants d ON s.parent_id = d.id
)
SELECT descendants.id, MAX(sv.time_updated) FROM descendants
LEFT JOIN session_v2 sv ON sv.id = descendants.id
GROUP BY descendants.id;`
	rows, err := db.QueryContext(ctx, query, m.Ref.ID)
	if err != nil {
		return nil, errors.New("cannot query opencode descendants")
	}
	defer func() { _ = rows.Close() }()

	var childIDs []string
	for rows.Next() {
		var (
			cid      string
			childUpd sql.NullInt64
		)
		if err := rows.Scan(&cid, &childUpd); err != nil {
			return nil, errors.New("cannot read opencode descendants")
		}
		if childUpd.Valid {
			updated := time.UnixMilli(childUpd.Int64).UTC()
			if om.now().Sub(updated) < recentWindow {
				return nil, fmt.Errorf("%w: child session was active within 10 minutes", ErrLive)
			}
		}
		if cid == m.Ref.ID {
			return nil, errors.New("session graph contains a cycle")
		}
		childIDs = append(childIDs, cid)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("cannot iterate opencode descendants")
	}

	return childIDs, nil
}

// deleteSession invokes `opencode session delete --standalone <id>`.
func (om *opencodeManager) deleteSession(ctx context.Context, id string) error {
	if strings.ContainsAny(id, "\x00\r\n\t ;&|`$") || strings.HasPrefix(id, "-") || !isSafeID(id) {
		return errors.New("invalid opencode session id")
	}
	if !om.canTargetRoot() {
		return errors.New("opencode root is non-standard; refusing to execute CLI")
	}

	xdgDataHome := filepath.Dir(filepath.Clean(om.root))
	env := []string{
		"XDG_DATA_HOME=" + xdgDataHome,
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_MODELS_FETCH=1",
	}

	return om.exec(ctx, []string{"opencode", "session", "delete", "--standalone", id}, om.root, env)
}

// isSafeID rejects anything outside a conservative ID charset so no option
// or injection shape reaches the CLI argv.
func isSafeID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}
