package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func commitOne(t *testing.T, db *DB, id string) {
	t.Helper()
	err := db.CommitScan(context.Background(), model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: id}, Title: id}},
		State:   provider.ScanState{Cursor: "c-" + id, Sources: map[string]provider.SourceState{}},
	})
	if err != nil {
		t.Fatalf("CommitScan: %v", err)
	}
}

func TestOpenUsesSchemaFileName(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if want := filepath.Join(dir, FileName(SchemaVersion(), "")); db.Path() != want {
		t.Errorf("path = %q, want %q", db.Path(), want)
	}
	if SchemaVersion() < 1 {
		t.Errorf("SchemaVersion = %d", SchemaVersion())
	}
}

func TestOpenSeedsFromLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	commitOne(t, db, "old")
	_ = db.Close()
	if err := os.Rename(db.Path(), filepath.Join(dir, legacyFileName)); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(db.Path() + "-wal")
	_ = os.Remove(db.Path() + "-shm")

	seeded, err := Open(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seeded.Close() }()
	metas, err := seeded.LoadCatalog(t.Context())
	if err != nil || len(metas) != 1 || metas[0].Ref.ID != "old" {
		t.Fatalf("seeded catalog = %+v, %v", metas, err)
	}
	fi, err := os.Stat(seeded.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("seeded mode = %04o", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, legacyFileName)); err != nil {
		t.Errorf("legacy database removed while seeding: %v", err)
	}
}

func TestOpenReadOnly(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenReadOnly(t.Context(), dir, ""); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("before any writer: err = %v, want ErrNoIndex", err)
	}

	w, err := Open(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	commitOne(t, w, "s1")

	r, err := OpenReadOnly(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	metas, states, err := r.LoadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Ref.ID != "s1" {
		t.Errorf("snapshot catalog = %+v", metas)
	}
	if states[model.AgentClaude].Cursor != "c-s1" {
		t.Errorf("snapshot states = %+v", states)
	}

	// The reader sees the writer's later commits.
	commitOne(t, w, "s2")
	if metas, _ := r.LoadCatalog(t.Context()); len(metas) != 2 {
		t.Errorf("reader after second commit: %d sessions", len(metas))
	}

	if err := r.CommitScan(t.Context(), model.AgentClaude, provider.ScanResult{}); !errors.Is(err, errReadOnly) {
		t.Errorf("CommitScan on reader: %v", err)
	}
	if err := r.DeleteSessions(t.Context(), []model.SessionRef{{Agent: model.AgentClaude, ID: "s1"}}); !errors.Is(err, errReadOnly) {
		t.Errorf("DeleteSessions on reader: %v", err)
	}
	if err := r.Rebuild(t.Context()); !errors.Is(err, errReadOnly) {
		t.Errorf("Rebuild on reader: %v", err)
	}
	if _, err := r.SQLDB().Exec("DELETE FROM sessions"); err == nil {
		t.Error("raw write through the read-only handle succeeded")
	}
	if r.Replaced() {
		t.Error("Replaced before any rebuild")
	}

	if err := w.Rebuild(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !r.Replaced() {
		t.Error("Replaced = false after the writer rebuilt the file")
	}
}

func TestTryLockIsPerSchema(t *testing.T) {
	dir := t.TempDir()
	l, err := TryLock(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(dir, ""); !errors.Is(err, ErrLocked) {
		t.Errorf("second TryLock: err = %v, want ErrLocked", err)
	}
	other, err := tryLockFile(dir, SchemaVersion()+1, "")
	if err != nil {
		t.Errorf("other schema's lock: %v", err)
	}
	_ = other.Unlock()
	_ = l.Unlock()
	l, err = TryLock(dir, "")
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	_ = l.Unlock()
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * UnusedFor)
	write := func(name string, mtime time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	own := write(FileName(SchemaVersion(), ""), old)
	stale := write(FileName(SchemaVersion()+1, ""), old)
	staleWAL := write(FileName(SchemaVersion()+1, "")+"-wal", old)
	recent := write(FileName(SchemaVersion()+2, ""), time.Now())
	locked := write(FileName(SchemaVersion()+3, ""), old)
	legacy := write(legacyFileName, old)
	unrelated := write("notes.txt", old)

	held, err := tryLockFile(dir, SchemaVersion()+3, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Unlock() }()

	Cleanup(dir, "", UnusedFor)

	for _, p := range []string{stale, staleWAL, legacy} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s kept: %v", filepath.Base(p), err)
		}
	}
	for _, p := range []string{own, recent, locked, unrelated} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, lockFileName(SchemaVersion()+1, ""))); err != nil {
		t.Errorf("lock file removed: %v", err)
	}
}

func TestParseIndexFile(t *testing.T) {
	type want struct {
		schema int
		key    string
	}
	for name, w := range map[string]want{
		"index-v1.db": {1, ""}, "index-v12.db": {12, ""},
		"index-v1-0a1b2c3d4e5f.db": {1, "0a1b2c3d4e5f"},
		"index-v0.db":              {}, "index-v01.db": {}, "index-v1.db-wal": {},
		"index-v.db": {}, "index.db": {}, "index-v-1.db": {},
		"index-v1-.db": {}, "index-v1-XYZ.db": {}, "index-v1-ab-cd.db": {},
	} {
		schema, key, ok := parseIndexFile(name)
		if ok != (w.schema > 0) || schema != w.schema || key != w.key {
			t.Errorf("parseIndexFile(%q) = %d, %q, %v", name, schema, key, ok)
		}
	}
}

func TestRootsKey(t *testing.T) {
	a := RootsKey("/home/u/.claude", "", "/home/u/.local/share/opencode")
	if len(a) != 12 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("RootsKey = %q", a)
	}
	if b := RootsKey("/home/u/.claude/", "", "/home/u/.local/share/opencode"); b != a {
		t.Errorf("cleaned roots give another key: %q vs %q", b, a)
	}
	for _, other := range []string{
		RootsKey("/home/u/.claude2", "", "/home/u/.local/share/opencode"),
		RootsKey("", "/home/u/.claude", "/home/u/.local/share/opencode"),
	} {
		if other == a {
			t.Errorf("different roots share key %q", a)
		}
	}
}

// Another roots key's database is its own writer's: only its lock and its
// age let Cleanup remove it, and it never seeds this key's database.
func TestOtherKeysAreSeparate(t *testing.T) {
	dir := t.TempDir()
	other, err := Open(t.Context(), dir, "bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	commitOne(t, other, "theirs")
	_ = other.Close()

	mine, err := Open(t.Context(), dir, "aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mine.Close() }()
	if metas, err := mine.LoadCatalog(t.Context()); err != nil || len(metas) != 0 {
		t.Fatalf("new key seeded from another key: %v %v", metas, err)
	}

	otherPath := filepath.Join(dir, FileName(SchemaVersion(), "bbbbbbbbbbbb"))
	held, err := tryLockFile(dir, SchemaVersion(), "bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * UnusedFor)
	for _, p := range []string{otherPath, otherPath + "-wal"} {
		_ = os.Chtimes(p, old, old)
	}
	Cleanup(dir, "aaaaaaaaaaaa", UnusedFor)
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("locked database removed: %v", err)
	}
	_ = held.Unlock()
	Cleanup(dir, "aaaaaaaaaaaa", UnusedFor)
	if _, err := os.Stat(otherPath); !os.IsNotExist(err) {
		t.Fatalf("unused database kept: %v", err)
	}
	if _, err := os.Stat(mine.Path()); err != nil {
		t.Fatalf("own database removed: %v", err)
	}
}

// A seed copy is consistent while the source's writer is still running.
func TestVacuumIntoWhileSourceWriterRuns(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	commitOne(t, w, "live")

	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, FileName(SchemaVersion(), ""))
	if err := vacuumInto(t.Context(), w.Path(), dst, SchemaVersion()); err != nil {
		t.Fatal(err)
	}
	copyDB, err := Open(t.Context(), dstDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	if metas, _ := copyDB.LoadCatalog(t.Context()); len(metas) != 1 {
		t.Errorf("copy has %d sessions, want 1", len(metas))
	}
	if err := vacuumInto(t.Context(), w.Path(), filepath.Join(dstDir, "older.db"), SchemaVersion()-1); err == nil {
		t.Error("seeded an older schema from a newer database")
	}
}
