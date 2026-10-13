package manage

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/ginkcode/agent-sessions/internal/model"
)

func writeFile(t *testing.T, path, data string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// claudeLeftovers adds what Claude keeps for id outside its transcript and
// returns those paths in plan order.
func claudeLeftovers(t *testing.T, root, id, slug string) []string {
	t.Helper()
	return []string{
		filepath.Dir(writeFile(t, filepath.Join(root, "file-history", id, "abc@v1"), "old")),
		filepath.Dir(writeFile(t, filepath.Join(root, "session-env", id, "hook-0.sh"), "export A=1")),
		writeFile(t, filepath.Join(root, "todos", id+"-agent-"+id+".json"), "[]"),
		writeFile(t, filepath.Join(root, "debug", id+".txt"), "log"),
		writeFile(t, filepath.Join(root, "plans", slug+".md"), "# plan"),
		writeFile(t, filepath.Join(root, "plans", slug+"-agent-a1.md"), "# agent plan"),
	}
}

func TestDelete_Claude_TrashesLeftovers(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	main := catalog[0]
	writeFile(t, main.SourcePath, `{"type":"user","slug":"happy-blue-fox","message":{"input":{"slug":"not-a-top-level"}}}`+"\n")
	leftovers := claudeLeftovers(t, roots.Claude, id, "happy-blue-fox")
	// Neither another session's files nor a plan named in a nested field.
	other := writeFile(t, filepath.Join(roots.Claude, "todos", uuidB+"-agent-"+uuidB+".json"), "[]")
	nested := writeFile(t, filepath.Join(roots.Claude, "plans", "not-a-top-level.md"), "# plan")
	backdate(t, roots.Claude)

	m, ft := newTestManager(t, roots)
	enableAll(t, m)
	ref := []model.SessionRef{{Agent: model.AgentClaude, ID: id}}
	p, err := m.Preview(context.Background(), ref, catalog)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(filepath.Dir(main.SourcePath), id)
	want := append([]string{main.SourcePath, sessionDir}, leftovers...)
	if got := p.Items[0].Paths; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths:\n got %v\nwant %v", got, want)
	}
	rep, err := m.Delete(context.Background(), ref, catalog, p.Token)
	if err != nil || rep.Deleted != 1 {
		t.Fatalf("Delete: %+v, %v", rep, err)
	}
	if !reflect.DeepEqual(ft.calls, want) {
		t.Fatalf("trash calls:\n got %v\nwant %v", ft.calls, want)
	}
	for _, p := range []string{other, nested} {
		for _, call := range ft.calls {
			if call == p {
				t.Fatalf("trashed %s", p)
			}
		}
	}
}

func TestPreview_Claude_SharedPlanIsKept(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	main := catalog[0]
	writeFile(t, main.SourcePath, `{"slug":"shared-plan-slug"}`+"\n")
	// A resumed session carries the same slug.
	resumed := claudeMainMeta(roots.Claude, uuidB)
	writeFile(t, resumed.SourcePath, `{"slug":"shared-plan-slug"}`+"\n")
	plan := writeFile(t, filepath.Join(roots.Claude, "plans", "shared-plan-slug.md"), "# plan")
	backdate(t, roots.Claude)
	catalog = append(catalog, resumed)

	m, _ := newTestManager(t, roots)
	enableAll(t, m)
	refs := []model.SessionRef{{Agent: model.AgentClaude, ID: id}, {Agent: model.AgentClaude, ID: uuidB}}
	p, err := m.Preview(context.Background(), refs, catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range p.Items {
		for _, path := range item.Paths {
			if path == plan {
				t.Fatalf("%s lists the shared plan", item.Ref.ID)
			}
		}
	}
	// Deleting the first must not make the second's plan differ from its
	// preview.
	rep, err := m.Delete(context.Background(), refs, catalog, p.Token)
	if err != nil || rep.Deleted != 2 {
		t.Fatalf("Delete: %+v, %v", rep, err)
	}
}

func TestPreview_Claude_UnsafeLeftoverIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(roots.Claude, "session-env"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(roots.Claude, "session-env", id)); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t, roots)
	enableAll(t, m)
	p, err := m.Preview(context.Background(), []model.SessionRef{{Agent: model.AgentClaude, ID: id}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if p.Items[0].Blocked != "" || len(p.Items[0].Paths) != 2 {
		t.Fatalf("want the session without the symlink, got %+v", p.Items[0])
	}
}

func TestScanClaudeSlugs_AcrossReads(t *testing.T) {
	long := strings.Repeat("x", maxClaudeSlugLen+10)
	data := `{"slug":"one-two"}` + "\n" + `{"slug":"` + long + `"}{"slug":"Bad Slug"}{"slug":"three"}{"slug":"un`
	var got []string
	if err := scanClaudeSlugs(iotest.OneByteReader(strings.NewReader(data)), func(s string) { got = append(got, s) }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"one-two", "three"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDelete_OpenCode_RemovesSessionDiffs(t *testing.T) {
	roots := testRoots(t)
	catalog := opencodeFixtures(t, roots.OpenCodeData, [][3]int64{{1, -1, -1}, {2, 1, -1}, {3, -1, -1}})
	diffs := filepath.Join(roots.OpenCodeData, "storage", "session_diff")
	gone := []string{writeFile(t, filepath.Join(diffs, "1.json"), "[]"), writeFile(t, filepath.Join(diffs, "2.json"), "[]")}
	kept := writeFile(t, filepath.Join(diffs, "3.json"), "[]")
	exec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(exec.exec))
	enableAll(t, m)
	ref := []model.SessionRef{{Agent: model.AgentOpenCode, ID: "1"}}
	p, err := m.Preview(context.Background(), ref, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := m.Delete(context.Background(), ref, catalog, p.Token); err != nil || rep.Deleted != 1 {
		t.Fatalf("Delete: %+v, %v", rep, err)
	}
	for _, path := range gone {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", path)
		}
	}
	if _, err := os.Lstat(kept); err != nil {
		t.Fatalf("another session's diff was removed: %v", err)
	}
}

func TestPruneHistoryFile(t *testing.T) {
	path := writeFile(t, filepath.Join(t.TempDir(), "history.jsonl"),
		`{"display":"a","sessionId":"gone"}`+"\n"+
			`not json`+"\n"+
			`{"display":"b","sessionId":"kept"}`+"\n"+
			`{"display":"c","sessionId":"gone"}`)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := pruneHistoryFile(path, "sessionId", map[string]bool{"gone": true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if want := "not json\n" + `{"display":"b","sessionId":"kept"}` + "\n"; string(data) != want {
		t.Fatalf("got %q, want %q", data, want)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v, want 0640", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

func TestDelete_PrunesPromptHistoryWhenAgentIsStopped(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	codex := codexMeta(roots.Codex, uuidB)
	writeFile(t, codex.SourcePath, `{}`)
	catalog = append(catalog, codex)
	claudeHistory := writeFile(t, filepath.Join(roots.Claude, "history.jsonl"), `{"sessionId":"`+id+`"}`+"\n"+`{"sessionId":"other"}`+"\n")
	codexHistory := writeFile(t, filepath.Join(roots.Codex, "history.jsonl"), `{"session_id":"`+uuidB+`"}`+"\n"+`{"session_id":"other"}`+"\n")
	backdate(t, roots.Claude, roots.Codex)

	// Claude Code is running: its sessions can still be trashed, but its
	// history is left alone.
	m, _ := newTestManager(t, roots, WithExec((&recordingExec{}).exec),
		WithClaudeRunning(func(context.Context) (bool, error) { return true, nil }))
	if err := m.SetConfig(Config{Enabled: true, AllowPermanentDelete: true, PrunePromptHistory: true}); err != nil {
		t.Fatal(err)
	}
	refs := []model.SessionRef{{Agent: model.AgentClaude, ID: id}, {Agent: model.AgentCodex, ID: uuidB}}
	p, err := m.Preview(context.Background(), refs, catalog)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := m.Delete(context.Background(), refs, catalog, p.Token)
	if err != nil || rep.Deleted != 2 {
		t.Fatalf("Delete: %+v, %v", rep, err)
	}
	if len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "Claude Code") {
		t.Fatalf("notes = %v", rep.Notes)
	}
	if data, _ := os.ReadFile(claudeHistory); !strings.Contains(string(data), id) {
		t.Fatalf("Claude history changed while Claude runs: %q", data)
	}
	if data, _ := os.ReadFile(codexHistory); string(data) != `{"session_id":"other"}`+"\n" {
		t.Fatalf("Codex history = %q", data)
	}
}

func TestDelete_PrunesClaudeHistoryWhenStopped(t *testing.T) {
	roots := testRoots(t)
	id, catalog := claudeFixtures(t, roots.Claude)
	history := writeFile(t, filepath.Join(roots.Claude, "history.jsonl"), `{"sessionId":"`+id+`"}`+"\n"+`{"sessionId":"other"}`+"\n")
	backdate(t, roots.Claude)
	m, _ := newTestManager(t, roots, WithClaudeRunning(func(context.Context) (bool, error) { return false, nil }))
	if err := m.SetConfig(Config{Enabled: true, PrunePromptHistory: true}); err != nil {
		t.Fatal(err)
	}
	refs := []model.SessionRef{{Agent: model.AgentClaude, ID: id}}
	p, err := m.Preview(context.Background(), refs, catalog)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := m.Delete(context.Background(), refs, catalog, p.Token)
	if err != nil || rep.Deleted != 1 || len(rep.Notes) != 0 {
		t.Fatalf("Delete: %+v, %v", rep, err)
	}
	if data, _ := os.ReadFile(history); string(data) != `{"sessionId":"other"}`+"\n" {
		t.Fatalf("Claude history = %q", data)
	}
}
