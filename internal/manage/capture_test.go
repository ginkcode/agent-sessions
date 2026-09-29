package manage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// recordingExport is a fake `opencode session export`: it records argv, env
// and dir, and returns a body keyed by session id.
type recordingExport struct {
	calls [][]string
	envs  [][]string
	dirs  []string
	body  map[string][]byte
	fail  bool
}

func (r *recordingExport) export(_ context.Context, argv []string, dir string, env []string) ([]byte, error) {
	r.calls = append(r.calls, argv)
	r.envs = append(r.envs, env)
	r.dirs = append(r.dirs, dir)
	if r.fail {
		return nil, errors.New("export failed")
	}
	id := argv[len(argv)-1]
	body, ok := r.body[id]
	if !ok {
		return nil, errors.New("no export for " + id)
	}
	return body, nil
}

func TestCapture_Claude_IncludesDescendants(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	m, _ := newTestManager(t, roots)

	root := catalog[0]
	got, err := m.CaptureSession(context.Background(), root, catalog)
	if err != nil {
		t.Fatalf("CaptureSession: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want root + child, got %d", len(got))
	}
	if got[0].Ref.ID != id || got[0].ParentID != "" {
		t.Fatalf("root capture = %+v", got[0])
	}
	var rels []string
	for _, f := range got[0].Files {
		rels = append(rels, f.Rel)
		if !strings.HasPrefix(f.Path, roots.Claude) {
			t.Errorf("file %s escapes the claude root", f.Path)
		}
	}
	if strings.Join(rels, ",") != "projects/enc/"+id+".jsonl" {
		t.Fatalf("root files = %v", rels)
	}
	child := got[1]
	if child.ParentID != id || !strings.HasPrefix(child.Ref.ID, id+"/agent-") {
		t.Fatalf("child capture = %+v", child)
	}
	if len(child.Files) != 2 {
		t.Fatalf("want child jsonl + meta, got %+v", child.Files)
	}
	for _, f := range child.Files {
		if !strings.Contains(f.Rel, "/subagents/") {
			t.Errorf("child file %s is not under subagents", f.Rel)
		}
	}
}

func TestCapture_Claude_ProtectedNameRefused(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	secret := filepath.Join(filepath.Dir(catalog[0].SourcePath), id, "auth.json")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t, roots)

	if _, err := m.CaptureSession(context.Background(), catalog[0], catalog); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("want protected-name refusal, got %v", err)
	}
}

func TestCapture_Claude_RecentSessionStillCaptured(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	catalog[0].UpdatedAt = time.Now()
	m, _ := newTestManager(t, roots)

	got, err := m.CaptureSession(context.Background(), catalog[0], catalog)
	if err != nil {
		t.Fatalf("capture must ignore recency, got %v", err)
	}
	if len(got) == 0 || got[0].Ref.ID != id {
		t.Fatalf("capture = %+v", got)
	}
}

func TestCapture_Codex_IncludesDescendantRollout(t *testing.T) {
	roots := testRoots(t)
	if err := os.MkdirAll(roots.Codex, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := codexMeta(roots.Codex, uuidA)
	child := codexMeta(roots.Codex, uuidB)
	child.ParentID = uuidA
	for _, meta := range []model.SessionMeta{parent, child} {
		if err := os.MkdirAll(filepath.Dir(meta.SourcePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(meta.SourcePath, []byte(`{"id":"`+meta.Ref.ID+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := newTestManager(t, roots)

	got, err := m.CaptureSession(context.Background(), parent, []model.SessionMeta{parent, child})
	if err != nil {
		t.Fatalf("CaptureSession: %v", err)
	}
	if len(got) != 2 || got[0].Ref.ID != uuidA || got[1].Ref.ID != uuidB || got[1].ParentID != uuidA {
		t.Fatalf("capture = %+v", got)
	}
	if len(got[0].Files) != 1 || !strings.HasSuffix(got[0].Files[0].Rel, uuidA+".jsonl") {
		t.Fatalf("parent rollout = %+v", got[0].Files)
	}
	if got[0].Files[0].Size == 0 {
		t.Fatal("rollout size not reported")
	}
}

func TestCapture_OpenCode_ExportsEachDescendant(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{
		{1, -1, -1},
		{2, 1, -1},
	})
	exp := &recordingExport{body: map[string][]byte{
		"1": []byte(`{"info":{"id":"1"}}`),
		"2": []byte(`{"info":{"id":"2"}}`),
	}}
	m, _ := newTestManager(t, roots, WithOpenCodeExport(exp.export))

	got, err := m.CaptureSession(context.Background(), catalog[0], catalog)
	if err != nil {
		t.Fatalf("CaptureSession: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want root + child export, got %d", len(got))
	}
	if string(got[0].Body) != `{"info":{"id":"1"}}` || got[0].ParentID != "" {
		t.Fatalf("root export = %+v", got[0])
	}
	if string(got[1].Body) != `{"info":{"id":"2"}}` || got[1].ParentID != "1" {
		t.Fatalf("child export = %+v", got[1])
	}
	if len(exp.calls) != 2 {
		t.Fatalf("want 2 exports, got %v", exp.calls)
	}
	for i, id := range []string{"1", "2"} {
		want := "opencode session export --standalone " + id
		if strings.Join(exp.calls[i], " ") != want {
			t.Errorf("argv = %v, want %s", exp.calls[i], want)
		}
		env := strings.Join(exp.envs[i], "\n")
		if !strings.Contains(env, "XDG_DATA_HOME="+filepath.Dir(filepath.Clean(roots.OpenCodeData))) ||
			!strings.Contains(env, "OPENCODE_DISABLE_AUTOUPDATE=1") ||
			!strings.Contains(env, "OPENCODE_DISABLE_MODELS_FETCH=1") {
			t.Errorf("env = %v", exp.envs[i])
		}
		if exp.dirs[i] != roots.OpenCodeData {
			t.Errorf("dir = %s", exp.dirs[i])
		}
	}
}

func TestCapture_OpenCode_UnfinishedMigrationRefused(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	addOpenCodeV1(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}})
	db, err := sql.Open("sqlite", filepath.Join(roots.OpenCodeData, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE kv SET value='{"phase":"sessions","cursor":"1"}'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	exp := &recordingExport{body: map[string][]byte{"1": []byte(`{}`)}}
	m, _ := newTestManager(t, roots, WithOpenCodeExport(exp.export))
	if _, err := m.CaptureSession(context.Background(), catalog[0], catalog); err == nil || !strings.Contains(err.Error(), "migration is unfinished") {
		t.Fatalf("want migration refusal, got %v", err)
	}
	if len(exp.calls) != 0 {
		t.Fatalf("export ran despite unfinished migration: %v", exp.calls)
	}
}
