package manage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// OpenCodeWarning is the exact warning copy required by the safety model.
const OpenCodeWarning = "OpenCode will permanently delete both OpenCode 1.x and 2.x copies of this session and its child sessions. It will not go to Trash and cannot be restored."

type openCodeMember struct {
	ID       string `json:"id"`
	InV1     bool   `json:"inV1"`
	InV2     bool   `json:"inV2"`
	V2Target bool   `json:"v2Target"`
}

// opencodeManager handles permanent deletion across the v2 CLI and v1 SQL.
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

// plan verifies the session and all descendants are not recent, then records
// the generation membership that execution must reproduce.
func (om *opencodeManager) plan(ctx context.Context, m model.SessionMeta) ([]openCodeMember, error) {
	if m.Ref.Agent != model.AgentOpenCode {
		return nil, ErrUnsupportedAction
	}
	if !isSafeID(m.Ref.ID) {
		return nil, errors.New("invalid opencode session id")
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
	if err != nil {
		return nil, errors.New("cannot inspect opencode database")
	}
	hasV1, err := sqliteread.HasTable(ctx, db, "session")
	if err != nil {
		return nil, errors.New("cannot inspect opencode database")
	}
	if !hasV2 && !hasV1 {
		return nil, errors.New("opencode database missing session tables")
	}
	migrated := true
	if hasV1 {
		migrated, err = v1MigrationCompleted(ctx, db)
		if err != nil {
			return nil, errors.New("cannot inspect opencode migration state")
		}
	}

	nodes := make([]string, 0, 2)
	if hasV2 {
		nodes = append(nodes, "SELECT id, parent_id FROM session_v2")
	}
	if hasV1 {
		nodes = append(nodes, "SELECT id, parent_id FROM session")
	}
	v2Exists, v2Updated, v2Parent := "0", "NULL", "NULL"
	if hasV2 {
		v2Exists = "EXISTS(SELECT 1 FROM session_v2 WHERE id=d.id)"
		v2Updated = "(SELECT time_updated FROM session_v2 WHERE id=d.id)"
		v2Parent = "(SELECT parent_id FROM session_v2 WHERE id=d.id)"
	}
	v1Exists, v1Updated, v1Parent := "0", "NULL", "NULL"
	if hasV1 {
		v1Exists = "EXISTS(SELECT 1 FROM session WHERE id=d.id)"
		v1Updated = "(SELECT time_updated FROM session WHERE id=d.id)"
		v1Parent = "(SELECT parent_id FROM session WHERE id=d.id)"
	}
	query := `WITH RECURSIVE nodes(id,parent_id) AS (` + strings.Join(nodes, " UNION ") + `),
	descendants(id) AS (
		SELECT ? WHERE EXISTS(SELECT 1 FROM nodes WHERE id=?)
		UNION
		SELECT n.id FROM nodes AS n JOIN descendants AS d ON n.parent_id=d.id
	)
	SELECT d.id, ` + v2Exists + `, ` + v1Exists + `, ` + v2Updated + `, ` + v1Updated + `,
		` + v2Parent + `, ` + v1Parent + `
	FROM descendants AS d ORDER BY d.id`
	rows, err := db.QueryContext(ctx, query, m.Ref.ID, m.Ref.ID)
	if err != nil {
		return nil, errors.New("cannot query opencode descendants")
	}
	defer func() { _ = rows.Close() }()

	type rowInfo struct {
		member             openCodeMember
		v2Parent, v1Parent sql.NullString
	}
	var found []rowInfo
	now := om.now()
	for rows.Next() {
		var item rowInfo
		var v2Updated, v1Updated sql.NullInt64
		if err := rows.Scan(&item.member.ID, &item.member.InV2, &item.member.InV1, &v2Updated, &v1Updated, &item.v2Parent, &item.v1Parent); err != nil {
			return nil, errors.New("cannot read opencode descendants")
		}
		if !isSafeID(item.member.ID) {
			return nil, errors.New("invalid opencode descendant id")
		}
		newest := v2Updated
		if v1Updated.Valid && (!newest.Valid || v1Updated.Int64 > newest.Int64) {
			newest = v1Updated
		}
		if newest.Valid && now.Sub(time.UnixMilli(newest.Int64).UTC()) < recentWindow {
			if item.member.ID == m.Ref.ID {
				return nil, fmt.Errorf("%w: session was updated within 10 minutes", ErrLive)
			}
			return nil, fmt.Errorf("%w: child session was active within 10 minutes", ErrLive)
		}
		found = append(found, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("cannot iterate opencode descendants")
	}
	if len(found) == 0 {
		return nil, errors.New("session not found in opencode database")
	}

	present := make(map[string]openCodeMember, len(found))
	children := make(map[string][]string)
	for _, item := range found {
		present[item.member.ID] = item.member
	}
	for _, item := range found {
		for _, parent := range []sql.NullString{item.v2Parent, item.v1Parent} {
			if parent.Valid {
				if _, ok := present[parent.String]; ok {
					children[parent.String] = append(children[parent.String], item.member.ID)
				}
			}
		}
	}
	if graphHasCycle(m.Ref.ID, children) {
		return nil, errors.New("session graph contains a cycle")
	}

	// The CLI cascades only along v2 parent links, while membership follows
	// both generations. Target every v2 member whose v2 parent is not itself a
	// selected v2 member; the root falls out of this rule when it is in v2.
	for _, item := range found {
		member := present[item.member.ID]
		if !member.InV2 {
			continue
		}
		parentMember, parentSelected := present[item.v2Parent.String]
		if !item.v2Parent.Valid || !parentSelected || !parentMember.InV2 {
			member.V2Target = true
			present[member.ID] = member
		}
	}
	// The CLI starts an OpenCode server, which resumes an unfinished v1-to-v2
	// migration. That would copy v1-only members into session_v2 after this
	// plan, and the SQL step would then leave those copies behind.
	if !migrated {
		for _, member := range present {
			if member.V2Target {
				return nil, errors.New("OpenCode 1.x to 2.x migration is unfinished; open OpenCode once to complete it before deleting")
			}
		}
	}
	members := make([]openCodeMember, 0, len(present))
	for _, member := range present {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members, nil
}

// v1MigrationCompleted mirrors OpenCode 2.x: with a v1 session table present,
// the migration is finished only when kv records phase "completed".
func v1MigrationCompleted(ctx context.Context, db *sql.DB) (bool, error) {
	hasKV, err := sqliteread.HasTable(ctx, db, "kv")
	if err != nil || !hasKV {
		return false, err
	}
	var phase sql.NullString
	err = db.QueryRowContext(ctx, `SELECT CASE WHEN json_valid(value) THEN json_extract(value,'$.phase') END
		FROM kv WHERE key='migration.v1-v2'`).Scan(&phase)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return phase.String == "completed", nil
}

func graphHasCycle(root string, children map[string][]string) bool {
	const (
		visiting = 1
		visited  = 2
	)
	colors := make(map[string]int)
	var visit func(string) bool
	visit = func(id string) bool {
		if colors[id] == visiting {
			return true
		}
		if colors[id] == visited {
			return false
		}
		colors[id] = visiting
		for _, child := range children[id] {
			if visit(child) {
				return true
			}
		}
		colors[id] = visited
		return false
	}
	return visit(root)
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

// deleteV1Rows removes v1 rows for ids. eventIDs must be the v1-only subset:
// event_sequence belongs to v2 event sourcing, so rows for sessions that also
// exist (or just existed) in v2 are left to the CLI, including its durable
// session.deleted tombstone.
func (om *opencodeManager) deleteV1Rows(ctx context.Context, ids, eventIDs []string) error {
	if len(ids) == 0 {
		return nil
	}
	if !om.canTargetRoot() {
		return errors.New("opencode root is non-standard; refusing writable database access")
	}
	inIDs := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !isSafeID(id) {
			return errors.New("invalid opencode session id")
		}
		inIDs[id] = true
	}
	for _, id := range eventIDs {
		if !inIDs[id] {
			return errors.New("opencode event id outside deletion set")
		}
	}
	dbFile := om.dbPath()
	fi, err := os.Lstat(dbFile)
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New("opencode database is unavailable")
	}
	abs, err := filepath.Abs(dbFile)
	if err != nil {
		return errors.New("cannot resolve opencode database")
	}
	params := url.Values{
		"mode":    {"rw"},
		"_pragma": {"busy_timeout(5000)", "foreign_keys(1)"},
		"_txlock": {"immediate"},
	}
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: params.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return errors.New("cannot open opencode database writable")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		return errors.New("cannot open opencode database writable")
	}
	var foreignKeys int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		return errors.New("opencode database foreign keys are unavailable")
	}
	hasEvents, err := sqliteread.HasTable(ctx, db, "event_sequence")
	if err != nil {
		return errors.New("cannot inspect opencode event table")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("cannot begin opencode v1 deletion")
	}
	defer func() { _ = tx.Rollback() }()
	in := placeholders(len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM part WHERE session_id IN (`+in+`)`, args...); err != nil {
		return errors.New("cannot delete opencode v1 parts")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM session WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return errors.New("cannot delete opencode v1 sessions")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != int64(len(ids)) {
		return errors.New("opencode v1 session set changed before deletion")
	}
	if hasEvents && len(eventIDs) != 0 {
		eventArgs := make([]any, len(eventIDs))
		for i, id := range eventIDs {
			eventArgs[i] = id
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM event_sequence WHERE aggregate_id IN (`+placeholders(len(eventIDs))+`)`, eventArgs...); err != nil {
			return errors.New("cannot delete opencode v1 events")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("cannot commit opencode v1 deletion")
	}
	return nil
}

// placeholders returns n positional SQL parameters. The caller validates that
// n is non-zero before constructing an IN clause.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
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
