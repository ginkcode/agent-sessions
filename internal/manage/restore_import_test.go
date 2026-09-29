package manage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

func enableRestore(t *testing.T, m *Manager) {
	t.Helper()
	cfg, err := m.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.AllowRestore = true
	if err := m.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
}

func makeOpenCodeBundle(rootID, childID, cwd string) *bundle.Bundle {
	rootBody := []byte(`{"info":{"id":"` + rootID + `","directory":"` + cwd + `"},"messages":[{"id":"m1","role":"user","content":"Task"}]}`)
	rootRel := "export/" + rootID + ".json"
	rootZip := "native/opencode/" + rootRel
	sessions := []bundle.SessionManifest{{
		Ref: model.SessionRef{Agent: model.AgentOpenCode, ID: rootID},
		Native: []bundle.NativeFile{{
			Name: rootZip, RootRel: rootRel, Size: int64(len(rootBody)),
		}},
	}}
	native := map[string][]byte{rootZip: rootBody}

	if childID != "" {
		childBody := []byte(`{"info":{"id":"` + childID + `","directory":"` + cwd + `"},"messages":[]}`)
		childRel := "export/" + childID + ".json"
		childZip := "native/opencode/" + childRel
		sessions = append(sessions, bundle.SessionManifest{
			Ref:      model.SessionRef{Agent: model.AgentOpenCode, ID: childID},
			ParentID: rootID,
			Native: []bundle.NativeFile{{
				Name: childZip, RootRel: childRel, Size: int64(len(childBody)),
			}},
		})
		native[childZip] = childBody
	}

	return &bundle.Bundle{
		Manifest: bundle.Manifest{
			Format:    bundle.CurrentFormat,
			Version:   bundle.CurrentVersion,
			Profile:   bundle.ProfileComplete,
			CreatedAt: time.Now().UTC(),
			Source: bundle.SourceMeta{
				Agent: model.AgentOpenCode, ID: rootID, CWD: cwd, Title: "OpenCode restore",
			},
			Sessions: sessions,
		},
		Native: native,
	}
}

func TestRestore_OpenCode_ImportArgvAndEnv(t *testing.T) {
	roots := testRoots(t)
	if err := os.MkdirAll(roots.OpenCodeData, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(rec.exec))
	enableRestore(t, m)

	old := "/home/dev/original"
	target := "/home/dev/restored"
	b := makeOpenCodeBundle("ses_root", "ses_child", old)
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentOpenCode,
		TargetCWD:   target,
	}

	prev, err := m.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked != "" {
		t.Fatalf("unexpected block: %s", prev.Items[0].Blocked)
	}

	report, err := m.Restore(context.Background(), req, nil, prev.Token)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if report.Restored != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}

	if len(rec.calls) != 2 {
		t.Fatalf("want 2 imports (child then parent), got %d", len(rec.calls))
	}
	for i, id := range []string{"ses_child", "ses_root"} {
		argv := strings.Join(rec.calls[i], " ")
		want := "opencode session import --standalone --directory " + target
		if !strings.HasPrefix(argv, want+" ") {
			t.Errorf("argv = %q, want prefix %q", argv, want)
		}
		if !strings.HasSuffix(rec.calls[i][len(rec.calls[i])-1], id+".json") {
			t.Errorf("import file = %q, want %s.json", rec.calls[i][len(rec.calls[i])-1], id)
		}
		env := strings.Join(rec.envs[i], "\n")
		if !strings.Contains(env, "XDG_DATA_HOME="+filepath.Dir(filepath.Clean(roots.OpenCodeData))) ||
			!strings.Contains(env, "OPENCODE_DISABLE_AUTOUPDATE=1") ||
			!strings.Contains(env, "OPENCODE_DISABLE_MODELS_FETCH=1") {
			t.Errorf("env = %v", rec.envs[i])
		}
		if rec.dirs[i] != m.opencode.root {
			t.Errorf("dir = %s, want %s", rec.dirs[i], m.opencode.root)
		}
	}
}

func TestRestore_OpenCode_CollisionBlocks(t *testing.T) {
	roots := testRoots(t)
	if err := os.MkdirAll(roots.OpenCodeData, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := &recordingExec{}
	m, _ := newTestManager(t, roots, WithExec(rec.exec))
	enableRestore(t, m)

	b := makeOpenCodeBundle("ses_root", "", "/home/dev/work")
	catalog := []model.SessionMeta{opencodeMeta(roots.OpenCodeData, "ses_root")}
	req := RestoreRequest{Bundle: b, TargetAgent: model.AgentOpenCode, TargetCWD: "/home/dev/work"}

	prev, err := m.PreviewRestore(context.Background(), req, catalog)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked == "" {
		t.Fatal("expected collision block")
	}

	req.Collision = CollisionCopy
	if _, err := m.PreviewRestore(context.Background(), req, catalog); err == nil {
		t.Fatal("copy mode must be refused: opencode import preserves the session id")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("import ran despite collision: %v", rec.calls)
	}
}

func makeCodexBundle(id, cwd string) *bundle.Bundle {
	rollout := []byte(`{"timestamp":"2026-09-28T12:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","cli_version":"0.158.0"}}` + "\n" +
		`{"timestamp":"2026-09-28T12:00:01Z","type":"turn_context","payload":{"cwd":"` + cwd + `","model":"gpt-5-codex"}}` + "\n" +
		`{"timestamp":"2026-09-28T12:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Task"}]}}` + "\n")
	rel := "sessions/2026/09/28/rollout-2026-09-28T12-00-00-" + id + ".jsonl"
	zip := "native/codex/" + rel
	return &bundle.Bundle{
		Manifest: bundle.Manifest{
			Format:    bundle.CurrentFormat,
			Version:   bundle.CurrentVersion,
			Profile:   bundle.ProfileComplete,
			CreatedAt: time.Now().UTC(),
			Source: bundle.SourceMeta{
				Agent: model.AgentCodex, ID: id, CWD: cwd, Title: "Codex restore",
			},
			Sessions: []bundle.SessionManifest{{
				Ref: model.SessionRef{Agent: model.AgentCodex, ID: id},
				Native: []bundle.NativeFile{{
					Name: zip, RootRel: rel, Size: int64(len(rollout)),
				}},
			}},
		},
		Native: map[string][]byte{zip: rollout},
	}
}

func TestRestore_Codex_RewritesCWD(t *testing.T) {
	roots := testRoots(t)
	if err := os.MkdirAll(roots.Codex, 0o700); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	m, _ := newTestManager(t, roots, WithNow(func() time.Time { return fixed }))
	enableRestore(t, m)

	old := "/home/dev/original"
	target := "/home/dev/restored"
	b := makeCodexBundle(uuidA, old)
	req := RestoreRequest{Bundle: b, TargetAgent: model.AgentCodex, TargetCWD: target}

	prev, err := m.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked != "" || prev.Items[0].TargetRef.ID != uuidA {
		t.Fatalf("preview = %+v", prev.Items[0])
	}

	report, err := m.Restore(context.Background(), req, nil, prev.Token)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if report.Restored != 1 {
		t.Fatalf("report = %+v", report)
	}

	dest := filepath.Join(roots.Codex, "sessions", "2026", "09", "29", "rollout-restored-"+uuidA+".jsonl")
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat rollout: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Count(text, `"cwd":"`+target+`"`) != 2 {
		t.Errorf("want cwd rewritten in session_meta and turn_context:\n%s", text)
	}
	if strings.Contains(text, old) {
		t.Errorf("original cwd survived:\n%s", text)
	}
	if !strings.Contains(text, `"type":"response_item"`) {
		t.Errorf("response item dropped:\n%s", text)
	}
}

func TestRestore_Codex_CollisionCopyRemapsID(t *testing.T) {
	roots := testRoots(t)
	if err := os.MkdirAll(roots.Codex, 0o700); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t, roots)
	enableRestore(t, m)

	b := makeCodexBundle(uuidA, "/home/dev/work")
	catalog := []model.SessionMeta{codexMeta(roots.Codex, uuidA)}
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentCodex,
		TargetCWD:   "/home/dev/restored",
		Collision:   CollisionCopy,
	}

	prev, err := m.PreviewRestore(context.Background(), req, catalog)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if !prev.Items[0].IsCopy || prev.Items[0].TargetRef.ID == uuidA {
		t.Fatalf("want a copied session, got %+v", prev.Items[0])
	}
	req.CopyID = prev.CopyID

	report, err := m.Restore(context.Background(), req, catalog, prev.Token)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if report.Restored != 1 || report.CreatedRefs[0].ID != prev.CopyID {
		t.Fatalf("report = %+v", report)
	}
	body, err := os.ReadFile(report.Items[0].Created[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"id":"`+prev.CopyID+`"`) {
		t.Errorf("session_meta id not remapped:\n%s", body)
	}
	if strings.Contains(string(body), uuidA) {
		t.Errorf("original id survived:\n%s", body)
	}
}

func TestRestore_Codex_NoNative(t *testing.T) {
	roots := testRoots(t)
	if err := os.MkdirAll(roots.Codex, 0o700); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t, roots)
	enableRestore(t, m)

	b := makeCodexBundle(uuidA, "/home/dev/work")
	b.Manifest.Profile = bundle.ProfileShareSafe
	b.Manifest.Sessions[0].Native = nil
	b.Native = nil
	req := RestoreRequest{Bundle: b, TargetAgent: model.AgentCodex, TargetCWD: "/home/dev/work"}
	if _, err := m.PreviewRestore(context.Background(), req, nil); err != ErrRestoreNoNative {
		t.Fatalf("want ErrRestoreNoNative, got %v", err)
	}
}
