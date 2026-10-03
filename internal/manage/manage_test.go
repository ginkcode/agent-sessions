package manage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

// fakeTrash records gio trash calls without touching the disk.
type fakeTrash struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
}

func (f *fakeTrash) Trash(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[path]; err != nil {
		return err
	}
	f.calls = append(f.calls, path)
	return nil
}

func (f *fakeTrash) Name() string { return "fake" }

// fakeProcFS is a static process table.
type fakeProc struct{ argv map[int][]string }

func (f fakeProc) Cmdlines() (map[int][]string, error) { return f.argv, nil }

// recordingExec captures CLI invocations, optionally failing per binary.
type recordingExec struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
	dirs  []string
	fail  map[string]bool // keyed by argv[0]
}

func (r *recordingExec) exec(_ context.Context, argv []string, dir string, env []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, argv)
	r.envs = append(r.envs, env)
	r.dirs = append(r.dirs, dir)
	if r.fail[argv[0]] {
		return errors.New("provider command failed")
	}
	return nil
}

func (r *recordingExec) all() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recordingExec) envSnapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.envs))
	copy(out, r.envs)
	return out
}

func testRoots(t *testing.T) paths.Roots {
	t.Helper()
	return paths.Roots{
		Claude:       filepath.Join(t.TempDir(), "claude"),
		Codex:        filepath.Join(t.TempDir(), "codex"),
		OpenCodeData: filepath.Join(t.TempDir(), "opencode"),
		Config:       filepath.Join(t.TempDir(), "config"),
	}
}

func claudeMainMeta(root, id string) model.SessionMeta {
	return model.SessionMeta{
		Ref:        model.SessionRef{Agent: model.AgentClaude, ID: id},
		SourcePath: filepath.Join(root, "projects", "enc", id+".jsonl"),
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
		Title:      "main " + id,
	}
}

func claudeChildMeta(root, parentID, agentID string) model.SessionMeta {
	return model.SessionMeta{
		Ref:        model.SessionRef{Agent: model.AgentClaude, ID: parentID + "/agent-" + agentID},
		ParentID:   parentID,
		SourcePath: filepath.Join(root, "projects", "enc", parentID, "subagents", "agent-"+agentID+".jsonl"),
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
	}
}

func codexMeta(root, id string) model.SessionMeta {
	day := time.Now().UTC().Format("2006/01/02")
	return model.SessionMeta{
		Ref:        model.SessionRef{Agent: model.AgentCodex, ID: id},
		SourcePath: filepath.Join(root, "sessions", day, "rollout-2025t01-01-"+id+".jsonl"),
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
	}
}

func opencodeMeta(root, id string) model.SessionMeta {
	return model.SessionMeta{
		Ref:        model.SessionRef{Agent: model.AgentOpenCode, ID: id},
		SourcePath: filepath.Join(root, "opencode.db"),
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
	}
}

const (
	uuidA = "11111111-2222-3333-4444-555555555555"
	uuidB = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func newTestManager(t *testing.T, roots paths.Roots, opts ...Option) (*Manager, *fakeTrash) {
	t.Helper()
	if roots.Config == "" {
		roots.Config = t.TempDir()
	}
	cfgPath := filepath.Join(roots.Config, "config.toml")
	defaults := []Option{
		WithTrash(&fakeTrash{fail: make(map[string]error)}),
		WithProcFS(fakeProc{argv: map[int][]string{}}),
		WithLiveFunc(func(context.Context, string, string) (bool, error) { return false, nil }),
	}
	mgr, err := New(roots, cfgPath, append(defaults, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var ft *fakeTrash
	if mgr.claude != nil && mgr.claude.trash != nil {
		if f, ok := mgr.claude.trash.(*fakeTrash); ok {
			ft = f
		}
	}
	return mgr, ft
}

func enableAll(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.SetConfig(Config{Enabled: true, AllowPermanentDelete: true}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
}

// backdate walks root and pushes every entry's mtime past the recency
// guard window so fixtures do not trip the 10-minute liveness check.
func backdate(t *testing.T, roots ...string) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chtimes(p, old, old)
			}
			return nil
		})
	}
}

// opencodeFixtures creates a real read-only-queryable SQLite DB with the
// OpenCode v2 schema and returns catalog metas for the given sessions.
// Each session is a (id, parentID, timeUpdatedMs) triple; a negative parent
// means NULL, a negative updated time means "very old".
func opencodeFixtures(t *testing.T, root string, sessions [][3]int64) []model.SessionMeta {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "opencode.db")
	for _, extra := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + extra)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE session_v2 (
		id TEXT PRIMARY KEY,
		parent_id TEXT,
		time_updated INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	for _, s := range sessions {
		parent := sql.NullString{String: "", Valid: s[1] >= 0}
		updated := sql.NullInt64{Int64: old, Valid: s[2] >= 0}
		if s[1] >= 0 {
			parent.String = strconv.FormatInt(s[1], 10)
		}
		if s[2] >= 0 {
			updated.Int64 = s[2]
		}
		if _, err := db.Exec(`INSERT INTO session_v2 (id, parent_id, time_updated) VALUES (?, ?, ?)`,
			strconv.FormatInt(s[0], 10), parent, updated); err != nil {
			t.Fatal(err)
		}
	}
	metas := make([]model.SessionMeta, 0, len(sessions))
	for _, s := range sessions {
		metas = append(metas, opencodeMeta(root, strconv.FormatInt(s[0], 10)))
	}
	backdate(t, dbPath)
	return metas
}

func addOpenCodeV1(t *testing.T, root string, sessions [][3]int64) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			parent_id TEXT,
			time_updated INTEGER
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES session(id) ON DELETE CASCADE
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			FOREIGN KEY(message_id) REFERENCES message(id) ON DELETE CASCADE
		);
		CREATE TABLE todo (
			session_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			PRIMARY KEY(session_id, position),
			FOREIGN KEY(session_id) REFERENCES session(id) ON DELETE CASCADE
		);
		CREATE TABLE event_sequence (aggregate_id TEXT PRIMARY KEY);
		CREATE TABLE event (
			id TEXT PRIMARY KEY,
			aggregate_id TEXT NOT NULL,
			FOREIGN KEY(aggregate_id) REFERENCES event_sequence(aggregate_id) ON DELETE CASCADE
		);
		CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO kv (key, value) VALUES ('migration.v1-v2', '{"phase":"completed"}');`); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	for _, s := range sessions {
		parent := sql.NullString{Valid: s[1] >= 0}
		updated := sql.NullInt64{Int64: old, Valid: s[2] >= 0}
		if parent.Valid {
			parent.String = strconv.FormatInt(s[1], 10)
		}
		if updated.Valid {
			updated.Int64 = s[2]
		}
		id := strconv.FormatInt(s[0], 10)
		if _, err := db.Exec(`INSERT INTO session (id,parent_id,time_updated) VALUES (?,?,?)`, id, parent, updated); err != nil {
			t.Fatal(err)
		}
		messageID := "m-" + id
		if _, err := db.Exec(`INSERT INTO message (id,session_id) VALUES (?,?)`, messageID, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO part (id,message_id,session_id) VALUES (?,?,?)`, "p-"+id, messageID, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO todo (session_id,position) VALUES (?,0)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO event_sequence (aggregate_id) VALUES (?)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO event (id,aggregate_id) VALUES (?,?)`, "e-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
}

func v1OnlyOpenCodeFixtures(t *testing.T, root string, sessions [][3]int64) []model.SessionMeta {
	t.Helper()
	catalog := opencodeFixtures(t, root, nil)
	addOpenCodeV1(t, root, sessions)
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE session_v2`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	for _, s := range sessions {
		catalog = append(catalog, opencodeMeta(root, strconv.FormatInt(s[0], 10)))
	}
	return catalog
}

func countRows(t *testing.T, root, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func claudeFixtures(t *testing.T, root string) (mainID string, catalog []model.SessionMeta) {
	t.Helper()
	mainID = uuidA
	main := claudeMainMeta(root, mainID)
	if err := os.MkdirAll(filepath.Dir(main.SourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main.SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(filepath.Dir(main.SourcePath), mainID)
	if err := os.MkdirAll(filepath.Join(sessionDir, "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	child := claudeChildMeta(root, mainID, "a1")
	if err := os.WriteFile(child.SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "subagents", "agent-a1.meta.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, root)
	return mainID, []model.SessionMeta{main, child}
}

func TestPreview_Claude_MainIncludesSessionDir(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked != "" {
		t.Fatalf("want 1 unblocked item, got %+v", p.Items)
	}
	if len(p.Items[0].Paths) != 2 {
		t.Fatalf("want jsonl + session dir, got %v", p.Items[0].Paths)
	}
	if !p.Items[0].Reversible || p.Items[0].Action != ActionTrash {
		t.Fatalf("claude action must be reversible trash")
	}
}

func TestPreview_RefusesArbitrarySourcePath(t *testing.T) {
	roots := testRoots(t)
	outside := filepath.Join(t.TempDir(), "elsewhere", "unrelated.jsonl")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	forged := append(append([]model.SessionMeta(nil), catalog...), model.SessionMeta{
		Ref:        model.SessionRef{Agent: model.AgentClaude, ID: id + "2"},
		SourcePath: outside,
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
	})
	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id + "2"}}, forged)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked == "" {
		t.Fatalf("forged SourcePath must be blocked, got %+v", p.Items)
	}
}

func TestPreview_SymlinkAnywhereRefused(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	// Replace the source file with a symlink pointing outside the root.
	meta := catalog[0]
	outside := filepath.Join(t.TempDir(), "target.jsonl")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(meta.SourcePath); err != nil {
		t.Fatal(err)
	}
	platform.Symlink(t, outside, meta.SourcePath)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked == "" || (!strings.Contains(p.Items[0].Blocked, "symlink") && !strings.Contains(p.Items[0].Blocked, "outside")) {
		t.Fatalf("symlink must be blocked, got %+v", p.Items)
	}
}

func TestPreview_ProtectedNameRefused(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	meta := catalog[0]
	protected := filepath.Join(filepath.Dir(meta.SourcePath), meta.Ref.ID, "history.jsonl")
	if err := os.WriteFile(protected, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || !strings.Contains(p.Items[0].Blocked, "protected") {
		t.Fatalf("protected sibling must block the parent item, got %+v", p.Items)
	}
}

func TestPreview_LiveChildBlocksParent(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	child := catalog[1]
	m, _ := newTestManager(t, roots,
		// Only the child reports live; the parent itself is idle.
		WithLiveFunc(func(_ context.Context, agent, sessionID string) (bool, error) {
			return agent == string(model.AgentClaude) && sessionID == child.Ref.ID, nil
		}))
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(p.Items) != 1 || !strings.Contains(p.Items[0].Blocked, "child") {
		t.Fatalf("parent must be blocked by its live child, got %+v", p.Items)
	}
}

func TestPreview_ClaudeDetectorAbsentFailsClosed(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	// Deliberately no WithLiveFunc: the Claude detector is absent, so
	// liveness must fail closed rather than silently proceeding.
	cfgPath := filepath.Join(roots.Config, "config.toml")
	mgr, err := New(roots, cfgPath, WithTrash(&fakeTrash{fail: make(map[string]error)}), WithProcFS(fakeProc{}))
	if err != nil {
		t.Fatal(err)
	}
	m := mgr
	enableAll(t, m)

	// Touch an old mtime so the recency check passes and only the missing
	// detector stands between the item and deletion.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(catalog[0].SourcePath, old, old); err != nil {
		t.Fatal(err)
	}

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Items[0].Blocked == "" || !strings.Contains(p.Items[0].Blocked, ErrLive.Error()) {
		t.Fatalf("missing detector must fail closed, got %+v", p.Items[0])
	}
}

func TestPreview_ProcGuardBlocksCodexEvenWithLiveFunc(t *testing.T) {
	roots := testRoots(t)
	catalog := []model.SessionMeta{codexMeta(roots.Codex, uuidA)}
	if err := os.MkdirAll(filepath.Dir(catalog[0].SourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog[0].SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, roots.Codex)
	m, _ := newTestManager(t, roots,
		WithProcFS(fakeProc{argv: map[int][]string{100: {"codex", "exec"}}}),
		WithLiveFunc(func(context.Context, string, string) (bool, error) { return false, nil }))
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentCodex, ID: uuidA}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || !strings.Contains(p.Items[0].Blocked, ErrLive.Error()) {
		t.Fatalf("running codex process must block, got %+v", p.Items)
	}
}

func TestDelete_TokenBindsToPathsAndDescendants(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, ft := newTestManager(t, roots)
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	// Remove the child so the plan changes; the token must go stale.
	if err := os.Remove(catalog[1].SourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog, p.Token); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("changed plan must yield ErrPreviewStale, got %v", err)
	}

	// Restore and delete for real. The restored file needs an old mtime or the
	// recency guard will (correctly) block the child and stale the plan.
	if err := os.WriteFile(catalog[1].SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(catalog[1].SourcePath, old, old); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog, p.Token)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rep.Deleted != 1 || len(rep.Items) != 1 || !rep.Items[0].OK {
		t.Fatalf("unexpected report %+v", rep)
	}
	if len(rep.Forgotten) != 2 {
		t.Fatalf("forgotten must include the child, got %v", rep.Forgotten)
	}
	if len(ft.calls) != 2 {
		t.Fatalf("want 2 trash calls, got %v", ft.calls)
	}
}

func TestDelete_ExpiredTokenRejected(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	base := time.Now()
	m, _ := newTestManager(t, roots, WithNow(func() time.Time { return base }))
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	// Advance the clock past the TTL.
	m.now = func() time.Time { return base.Add(tokenTTL + time.Minute) }
	if _, err := m.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog, p.Token); !errors.Is(err, ErrPreviewStale) {
		t.Fatalf("expired token must be rejected, got %v", err)
	}
}

func TestDelete_UnknownRefFailsClosed(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	unknown := model.SessionRef{Agent: model.AgentClaude, ID: "nope"}
	if _, err := m.Delete(context.Background(), []model.SessionRef{unknown}, catalog, p.Token); err == nil {
		t.Fatal("unknown ref must fail closed")
	}
}

func TestDelete_CodexChildFirstOrder(t *testing.T) {
	roots := testRoots(t)
	child := codexMeta(roots.Codex, uuidB)
	child.ParentID = uuidA
	parent := codexMeta(roots.Codex, uuidA)
	catalog := []model.SessionMeta{parent, child}
	for _, m := range catalog {
		if err := os.MkdirAll(filepath.Dir(m.SourcePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.SourcePath, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backdate(t, roots.Codex)
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentCodex, ID: uuidA}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked != "" {
		t.Fatalf("want one unblocked parent, got %+v", p.Items)
	}
	if len(p.Items[0].Paths) != 2 {
		t.Fatalf("parent plan must include child rollout, got %v", p.Items[0].Paths)
	}

	rep, err := m.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentCodex, ID: uuidA}}, catalog, p.Token)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rep.Deleted != 1 || len(rep.Forgotten) != 2 {
		t.Fatalf("unexpected report %+v", rep)
	}
	calls := exec.all()
	if len(calls) != 2 {
		t.Fatalf("want child-then-parent CLI calls, got %v", calls)
	}
	if calls[0][3] != uuidB || calls[1][3] != uuidA {
		t.Fatalf("children must be deleted first, got %v", calls)
	}
	for _, argv := range calls {
		if strings.Join(argv, " ") != strings.Join([]string{"codex", "delete", "--force", argv[3]}, " ") {
			t.Fatalf("unexpected argv %v", argv)
		}
	}
}

func TestDelete_PartialFailureReportsRemaining(t *testing.T) {
	roots := testRoots(t)
	child := claudeChildMeta(roots.Claude, uuidA, "a1")
	main := claudeMainMeta(roots.Claude, uuidA)
	catalog := []model.SessionMeta{main, child}
	if err := os.MkdirAll(filepath.Dir(main.SourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main.SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(child.SourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child.SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, roots.Claude)

	ft := &fakeTrash{fail: map[string]error{
		filepath.Join(filepath.Dir(main.SourcePath), main.Ref.ID): errors.New("gio failed"),
	}}
	mgr, err := New(roots, filepath.Join(roots.Config, "config.toml"), WithTrash(ft), WithProcFS(fakeProc{}),
		WithLiveFunc(func(context.Context, string, string) (bool, error) { return false, nil }))
	if err != nil {
		t.Fatal(err)
	}
	enableAll(t, mgr)

	p, err := mgr.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: uuidA}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	rep, err := mgr.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: uuidA}}, catalog, p.Token)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rep.Deleted != 0 || rep.Failed != 1 {
		t.Fatalf("child failure must fail the parent item: %+v", rep)
	}
	if len(rep.Items[0].Remaining) == 0 {
		t.Fatal("remaining paths must be reported")
	}
	// The .jsonl parent may already be trashed; nothing outside the plan.
	for _, moved := range rep.Items[0].Moved {
		if !strings.HasPrefix(moved, roots.Claude) {
			t.Fatalf("moved path escaped root: %q", moved)
		}
	}
}

func TestConfig_Matrix(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)

	cases := []struct {
		enabled, perm  bool
		agent          model.AgentID
		sessionID      string
		wantPreviewErr error
	}{
		{false, false, model.AgentClaude, id, ErrDisabled},
		{true, false, model.AgentClaude, id, nil},
		{true, false, model.AgentCodex, uuidA, ErrPermanentNotAllowed},
		{true, true, model.AgentCodex, uuidA, nil},
		{true, false, model.AgentOpenCode, "oc1", ErrPermanentNotAllowed},
		{true, true, model.AgentOpenCode, "oc1", nil},
	}
	for _, tc := range cases {
		if err := m.SetConfig(Config{Enabled: tc.enabled, AllowPermanentDelete: tc.perm}); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		all := append([]model.SessionMeta(nil), catalog...)
		if tc.agent == model.AgentCodex {
			mm := codexMeta(roots.Codex, tc.sessionID)
			if err := os.MkdirAll(filepath.Dir(mm.SourcePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mm.SourcePath, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-48 * time.Hour)
			if err := os.Chtimes(mm.SourcePath, old, old); err != nil {
				t.Fatal(err)
			}
			all = []model.SessionMeta{mm}
		}
		if tc.agent == model.AgentOpenCode {
			all = opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
		}
		sessionID := tc.sessionID
		if tc.agent == model.AgentOpenCode {
			sessionID = "1"
		}
		p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: tc.agent, ID: sessionID}}, all)
		switch {
		case tc.wantPreviewErr != nil && errors.Is(err, tc.wantPreviewErr):
			continue
		case tc.wantPreviewErr != nil:
			// ErrPermanentNotAllowed is surfaced per item (the plan continues
			// for other selections), so inspect the item's Blocked field.
			if err == nil && p != nil && len(p.Items) == 1 && strings.Contains(p.Items[0].Blocked, tc.wantPreviewErr.Error()) {
				continue
			}
			t.Errorf("cfg %+v: got err=%v items=%+v, want %v", tc, err, p.Items, tc.wantPreviewErr)
		case err == nil:
			if p == nil || len(p.Items) != 1 || p.Items[0].Blocked != "" {
				t.Errorf("cfg %+v: unexpected blocked item, items=%+v", tc, p.Items)
			}
		default:
			t.Errorf("cfg %+v: got %v, want nil", tc, err)
		}
	}
}

func TestNew_ToleratesMissingRoots(t *testing.T) {
	roots := paths.Roots{
		Claude:       filepath.Join(t.TempDir(), "absent-claude"),
		Codex:        filepath.Join(t.TempDir(), "absent-codex"),
		OpenCodeData: filepath.Join(t.TempDir(), "absent-oc"),
	}
	m, err := New(roots, filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("New with absent roots must succeed lazily, got %v", err)
	}
	if _, err := m.Config(); err != nil {
		t.Fatalf("Config: %v", err)
	}
}

func TestPathSafety_RejectsEscapeAndRoot(t *testing.T) {
	root := t.TempDir()
	ps, err := newPathSafety(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.classify(root); err == nil {
		t.Fatal("root itself must be refused")
	}
	outside := filepath.Join(filepath.Dir(root), "x.jsonl")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.classify(outside); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("outside must be refused, got %v", err)
	}
}

func TestPreview_Claude_ChildPathRequiresParentBoundary(t *testing.T) {
	// The subagents directory must sit inside a directory named exactly
	// <ParentID>, not merely end with that suffix (parent "abc" must not
	// match ".../xabc/subagents").
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	// Forge a child whose parent directory is named "<id>extra".
	forgedParent := filepath.Join(roots.Claude, "projects", "enc", id+"extra", "subagents")
	if err := os.MkdirAll(forgedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	forged := append(append([]model.SessionMeta(nil), catalog...), model.SessionMeta{
		Ref:        model.SessionRef{Agent: model.AgentClaude, ID: id + "/agent-zz"},
		ParentID:   id,
		SourcePath: filepath.Join(forgedParent, "agent-zz.jsonl"),
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
	})
	forgedPath := filepath.Join(forgedParent, "agent-zz.jsonl")
	if err := os.WriteFile(forgedPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, roots.Claude)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id + "/agent-zz"}}, forged)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked == "" {
		t.Fatalf("child under a misnamed parent directory must be blocked, got %+v", p.Items)
	}
}

func TestPreview_OpenCode_DescendantsAndRecency(t *testing.T) {
	roots := testRoots(t)
	// Session 2 and 3 are children of 1; 3 was updated seconds ago and must
	// block deletion of the root through the descendant recency check.
	fresh := time.Now().Add(-1 * time.Minute).UnixMilli()
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{
		{1, -1, -1},
		{2, 1, -1},
		{3, 1, fresh},
	})
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || !strings.Contains(p.Items[0].Blocked, ErrLive.Error()) {
		t.Fatalf("recent child must block the root, got %+v", p.Items)
	}

	// Age the child; the root is now deletable and both children are
	// descendants bound into the token.
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	opencodeSetUpdated(t, roots.OpenCodeData, "3", old)
	p, err = m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked != "" {
		t.Fatalf("aged child must not block the root, got %+v", p.Items)
	}
}

func opencodeSetUpdated(t *testing.T, root, id string, updatedMs int64) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE session_v2 SET time_updated=? WHERE id=?`, updatedMs, id); err != nil {
		t.Fatal(err)
	}
}

func TestPreview_OpenCode_CycleSafe(t *testing.T) {
	roots := testRoots(t)
	// 1 -> 2 -> 1 forms a parent_id cycle; the query must terminate and the
	// item must be blocked rather than hang or crash.
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{
		{1, 2, -1},
		{2, 1, -1},
	})
	m, _ := newTestManager(t, roots)
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 {
		t.Fatalf("want one item, got %+v", p.Items)
	}
	// A cyclic graph must not be deletable: the cycle is also a descendant
	// chain that includes the selected root itself.
	if p.Items[0].Blocked == "" {
		t.Fatalf("cycle must be blocked, got %+v", p.Items[0])
	}
}

func TestDelete_Claude_TrashesJsonlBeforeSessionDir(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, ft := newTestManager(t, roots)
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	rep, err := m.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog, p.Token)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rep.Deleted != 1 {
		t.Fatalf("want 1 deleted, got %+v", rep)
	}
	calls := ft.calls
	if len(calls) != 2 {
		t.Fatalf("want 2 trash calls, got %v", calls)
	}
	if filepath.Ext(calls[0]) != ".jsonl" || filepath.Ext(calls[1]) != "" {
		t.Fatalf("jsonl must be trashed before the session dir, got %v", calls)
	}
}

func TestDelete_OpenCode_ExecutesScopedCLI(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{
		{1, -1, -1},
		{2, 1, -1},
	})
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Items[0].Warning == "" || !strings.Contains(p.Items[0].Warning, "permanently delete") {
		t.Fatalf("opencode items must carry the warning, got %+v", p.Items[0])
	}
	rep, err := m.Delete(context.Background(), []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}, catalog, p.Token)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rep.Deleted != 1 {
		t.Fatalf("want 1 deleted, got %+v", rep)
	}
	calls, envs := exec.all(), exec.envSnapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 CLI call, got %v", calls)
	}
	want := []string{"opencode", "session", "delete", "--standalone", "1"}
	if strings.Join(calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("unexpected argv %v", calls[0])
	}
	env := strings.Join(envs[0], "\n")
	if !strings.Contains(env, "XDG_DATA_HOME="+filepath.Dir(filepath.Clean(roots.OpenCodeData))) ||
		!strings.Contains(env, "OPENCODE_DISABLE_AUTOUPDATE=1") ||
		!strings.Contains(env, "OPENCODE_DISABLE_MODELS_FETCH=1") ||
		strings.Contains(env, "OPENCODE_DISABLE_PROMPT_TITLE") ||
		strings.Contains(env, "OPENCODE_CONFIG_GLOBAL_PATH") {
		t.Fatalf("unexpected env %v", envs[0])
	}
	if len(rep.Forgotten) != 2 {
		t.Fatalf("forgotten must include the child, got %v", rep.Forgotten)
	}
}

func TestDelete_OpenCode_CLIFailureIsReported(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	exec := &recordingExec{fail: map[string]bool{"opencode": true}}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)

	refs := []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}
	p, err := m.Preview(context.Background(), refs, catalog)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	rep, err := m.Delete(context.Background(), refs, catalog, p.Token)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rep.Failed != 1 || rep.Items[0].OK {
		t.Fatalf("want 1 failure, got %+v", rep)
	}
	if got, want := rep.Items[0].Error, "OpenCode 2.x deletion failed: provider command failed"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestDelete_OpenCode_V1OnlyUsesSQL(t *testing.T) {
	roots := testRoots(t)
	catalog := v1OnlyOpenCodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}, {2, 1, -1}})
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "1"}
	preview, err := m.Preview(context.Background(), []model.SessionRef{ref}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.Delete(context.Background(), []model.SessionRef{ref}, catalog, preview.Token)
	if err != nil || report.Deleted != 1 {
		t.Fatalf("delete = %+v, %v", report, err)
	}
	if len(exec.all()) != 0 {
		t.Fatalf("v1-only delete invoked CLI: %v", exec.all())
	}
	for _, table := range []string{"session", "message", "part", "todo", "event_sequence", "event"} {
		if count := countRows(t, roots.OpenCodeData, table); count != 0 {
			t.Errorf("%s rows = %d", table, count)
		}
	}
}

func TestDelete_OpenCode_BothGenerations(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	addOpenCodeV1(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "1"}
	preview, err := m.Preview(context.Background(), []model.SessionRef{ref}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.Delete(context.Background(), []model.SessionRef{ref}, catalog, preview.Token)
	if err != nil || report.Deleted != 1 {
		t.Fatalf("delete = %+v, %v", report, err)
	}
	if calls := exec.all(); len(calls) != 1 || calls[0][len(calls[0])-1] != "1" {
		t.Fatalf("CLI calls = %v", calls)
	}
	if count := countRows(t, roots.OpenCodeData, "session"); count != 0 {
		t.Fatalf("v1 session rows = %d", count)
	}
	// event_sequence is v2 event-sourcing state; the CLI owns it (including
	// its session.deleted tombstone) for any session that exists in v2.
	if count := countRows(t, roots.OpenCodeData, "event_sequence"); count != 1 {
		t.Fatalf("v2-owned event_sequence rows = %d", count)
	}
}

func TestPreview_OpenCode_UnfinishedMigrationBlocksCLI(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	addOpenCodeV1(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}, {2, -1, -1}})
	catalog = append(catalog, opencodeMeta(roots.OpenCodeData, "2"))
	db, err := sql.Open("sqlite", filepath.Join(roots.OpenCodeData, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE kv SET value='{"phase":"sessions","cursor":"1"}'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	m, _ := newTestManager(t, roots)
	enableAll(t, m)
	preview, err := m.Preview(context.Background(), []model.SessionRef{
		{Agent: model.AgentOpenCode, ID: "1"}, {Agent: model.AgentOpenCode, ID: "2"},
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 2 {
		t.Fatalf("items = %+v", preview.Items)
	}
	for _, item := range preview.Items {
		switch item.Ref.ID {
		case "1":
			if !strings.Contains(item.Blocked, "migration is unfinished") {
				t.Errorf("v2 target not blocked: %+v", item)
			}
		case "2":
			// v1-only deletion needs no CLI, so the migration cannot run.
			if item.Blocked != "" {
				t.Errorf("v1-only item blocked: %+v", item)
			}
		}
	}
}

func TestDelete_OpenCode_V2RootWithV2MemberLinkedThroughV1(t *testing.T) {
	roots := testRoots(t)
	// In v2 both sessions are roots; only the v1 copy links 5 under 1.
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}, {5, -1, -1}})
	addOpenCodeV1(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}, {5, 1, -1}})
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "1"}
	preview, err := m.Preview(context.Background(), []model.SessionRef{ref}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.Delete(context.Background(), []model.SessionRef{ref}, catalog, preview.Token)
	if err != nil || report.Deleted != 1 || len(report.Forgotten) != 2 {
		t.Fatalf("delete = %+v, %v", report, err)
	}
	var targets []string
	for _, call := range exec.all() {
		targets = append(targets, call[len(call)-1])
	}
	sort.Strings(targets)
	if !reflect.DeepEqual(targets, []string{"1", "5"}) {
		t.Fatalf("CLI targets = %v", targets)
	}
}

func TestDelete_OpenCode_SpanningGenerationDescendants(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{2, 1, -1}, {3, 2, -1}})
	addOpenCodeV1(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}, {4, 1, -1}})
	catalog = append(catalog, opencodeMeta(roots.OpenCodeData, "1"), opencodeMeta(roots.OpenCodeData, "4"))
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "1"}
	preview, err := m.Preview(context.Background(), []model.SessionRef{ref}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.Delete(context.Background(), []model.SessionRef{ref}, catalog, preview.Token)
	if err != nil || report.Deleted != 1 || len(report.Forgotten) != 4 {
		t.Fatalf("delete = %+v, %v", report, err)
	}
	if calls := exec.all(); len(calls) != 1 || calls[0][len(calls[0])-1] != "2" {
		t.Fatalf("topmost v2 CLI calls = %v", calls)
	}
	if countRows(t, roots.OpenCodeData, "session") != 0 {
		t.Fatal("v1 descendants remain")
	}
}

func TestPreview_OpenCode_V1RecencyBlocks(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	addOpenCodeV1(t, roots.OpenCodeData, [][3]int64{{1, -1, time.Now().Add(-time.Minute).UnixMilli()}})
	m, _ := newTestManager(t, roots)
	enableAll(t, m)
	preview, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || !strings.Contains(preview.Items[0].Blocked, ErrLive.Error()) {
		t.Fatalf("recent v1 duplicate not blocked: %+v", preview.Items)
	}
}

func TestDelete_OpenCode_V1SQLFailure(t *testing.T) {
	roots := testRoots(t)
	catalog := v1OnlyOpenCodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	m, _ := newTestManager(t, roots)
	enableAll(t, m)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "1"}
	preview, err := m.Preview(context.Background(), []model.SessionRef{ref}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(roots.OpenCodeData, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE part`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	report, err := m.Delete(context.Background(), []model.SessionRef{ref}, catalog, preview.Token)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 || report.Deleted != 0 || len(report.Forgotten) != 0 {
		t.Fatalf("SQL failure report = %+v", report)
	}
	if countRows(t, roots.OpenCodeData, "session") != 1 {
		t.Fatal("failed SQL delete removed the session")
	}
}

func TestDelete_OpenCode_InvalidIDRefusedBeforeCLI(t *testing.T) {
	exec := &recordingExec{}
	om, err := newOpenCodeManager(filepath.Join(t.TempDir(), "opencode"), exec.exec, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "-h", "a b", "a;rm", "a\nb", strings.Repeat("x", 129)} {
		if err := om.deleteSession(context.Background(), id); err == nil {
			t.Fatalf("id %q must be refused", id)
		}
	}
	if len(exec.all()) != 0 {
		t.Fatalf("no CLI call may happen for invalid ids, got %v", exec.all())
	}
}

func TestProcLive_OpenCodeServersIgnored(t *testing.T) {
	g := newLiveGuard(fakeProc{argv: map[int][]string{
		1: {"/home/u/.opencode/bin/opencode", "serve", "--service"},
		2: {"/home/u/.local/share/zed/external_agents/registry/opencode/v1/opencode", "acp"},
	}})
	live, err := g.procLive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if live["opencode"] {
		t.Fatalf("opencode serve/acp must not count as live, got %v", live)
	}

	g2 := newLiveGuard(fakeProc{argv: map[int][]string{3: {"opencode"}}})
	live2, err := g2.procLive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !live2["opencode"] {
		t.Fatalf("interactive opencode must count as live, got %v", live2)
	}
}

func TestPreview_DaemonsExcludedFromProcScan(t *testing.T) {
	// codex app-server and opencode serve host many sessions; they must not
	// block deletion the way an interactive process does.
	roots := testRoots(t)
	catalog := []model.SessionMeta{codexMeta(roots.Codex, uuidA)}
	if err := os.MkdirAll(filepath.Dir(catalog[0].SourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog[0].SourcePath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, roots.Codex)
	m, _ := newTestManager(t, roots,
		WithProcFS(fakeProc{argv: map[int][]string{
			1: {"codex", "app-server"},
			2: {"/usr/bin/node", "opencode", "serve"},
		}}),
		WithLiveFunc(func(context.Context, string, string) (bool, error) { return false, nil }))
	enableAll(t, m)

	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentCodex, ID: uuidA}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked != "" {
		t.Fatalf("daemon processes must not block, got %+v", p.Items)
	}

	// The same scan with an interactive codex process must block.
	m2, _ := newTestManager(t, roots,
		WithProcFS(fakeProc{argv: map[int][]string{3: {"codex", "exec"}}}),
		WithLiveFunc(func(context.Context, string, string) (bool, error) { return false, nil }))
	p2, err := m2.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentCodex, ID: uuidA}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Items) != 1 || !strings.Contains(p2.Items[0].Blocked, ErrLive.Error()) {
		t.Fatalf("interactive codex must block, got %+v", p2.Items)
	}
}
