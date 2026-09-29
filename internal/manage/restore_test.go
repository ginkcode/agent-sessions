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
	"github.com/ginkcode/agent-sessions/internal/paths"
)

func prepareClaudeRoot(t *testing.T, roots paths.Roots) {
	t.Helper()
	if err := os.MkdirAll(roots.Claude, 0o700); err != nil {
		t.Fatalf("mkdir claude root: %v", err)
	}
}

func makeClaudeBundle(t *testing.T, profile bundle.Profile, rootID, oldCWD string) *bundle.Bundle {
	t.Helper()
	var native map[string][]byte
	var sessions []bundle.SessionManifest

	if profile == bundle.ProfileComplete {
		encOld := EncodeClaudeProjectDir(oldCWD)
		mainRel := "projects/" + encOld + "/" + rootID + ".jsonl"
		mainZip := "native/claude-code/" + mainRel
		mainData := []byte(`{"type":"user","uuid":"u1","sessionId":"` + rootID + `","cwd":"` + oldCWD + `","message":{"role":"user","content":"Task 1"}}` + "\n" +
			`{"type":"assistant","uuid":"a1","sessionId":"` + rootID + `","cwd":"` + oldCWD + `","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"Done"}]}}` + "\n")

		subRel := "projects/" + encOld + "/" + rootID + "/subagents/agent-sub1.jsonl"
		subZip := "native/claude-code/" + subRel
		subData := []byte(`{"type":"user","uuid":"su1","sessionId":"` + rootID + `","agentId":"sub1","cwd":"` + oldCWD + `","message":{"role":"user","content":"Subtask 1"}}` + "\n")

		subMetaRel := "projects/" + encOld + "/" + rootID + "/subagents/agent-sub1.meta.json"
		subMetaZip := "native/claude-code/" + subMetaRel
		subMetaData := []byte(`{"agentType":"Explore","description":"Sub search"}`)

		native = map[string][]byte{
			mainZip:    mainData,
			subZip:     subData,
			subMetaZip: subMetaData,
		}

		sessions = []bundle.SessionManifest{
			{
				Ref: model.SessionRef{Agent: model.AgentClaude, ID: rootID},
				Native: []bundle.NativeFile{
					{Name: mainZip, RootRel: mainRel, Size: int64(len(mainData))},
				},
			},
			{
				Ref:      model.SessionRef{Agent: model.AgentClaude, ID: rootID + "/agent-sub1"},
				ParentID: rootID,
				Native: []bundle.NativeFile{
					{Name: subZip, RootRel: subRel, Size: int64(len(subData))},
					{Name: subMetaZip, RootRel: subMetaRel, Size: int64(len(subMetaData))},
				},
			},
		}
	}

	return &bundle.Bundle{
		Manifest: bundle.Manifest{
			Format:    bundle.CurrentFormat,
			Version:   bundle.CurrentVersion,
			Profile:   profile,
			CreatedAt: time.Now().UTC(),
			Source: bundle.SourceMeta{
				Agent: model.AgentClaude,
				ID:    rootID,
				CWD:   oldCWD,
				Title: "Test Restore Session",
			},
			Sessions: sessions,
		},
		Native: native,
	}
}

func TestEncodeClaudeProjectDir(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/home/user/work/project", "-home-user-work-project"},
		{"/home/user/.config/repo", "-home-user--config-repo"},
		{"/home/user/space repo", "-home-user-space-repo"},
		{"/home/user/a__b", "-home-user-a--b"},
		{"/home/user/ü_world", "-home-user---world"},
	}

	for _, tc := range cases {
		got := EncodeClaudeProjectDir(tc.input)
		if got != tc.want {
			t.Errorf("EncodeClaudeProjectDir(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestRestore_AllowRestoreGating(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mgr, _ := newTestManager(t, roots)

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, "/home/dev/work")
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   "/home/dev/restored",
	}

	// 1. Gated off by default
	_, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != ErrRestoreNotAllowed {
		t.Fatalf("expected ErrRestoreNotAllowed, got %v", err)
	}

	_, err = mgr.Restore(context.Background(), req, nil, "any-token")
	if err != ErrRestoreNotAllowed {
		t.Fatalf("expected ErrRestoreNotAllowed, got %v", err)
	}

	// 2. Enable allow_restore
	cfg, err := mgr.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cfg.AllowRestore = true
	if err := mgr.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	// 3. Now preview succeeds
	prev, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore failed: %v", err)
	}
	if len(prev.Items) != 1 {
		t.Fatalf("expected 1 preview item, got %d", len(prev.Items))
	}
	if prev.Token == "" {
		t.Errorf("expected non-empty token")
	}
}

func TestRestore_ShareSafeBundleRejected(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mgr, _ := newTestManager(t, roots)
	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	b := makeClaudeBundle(t, bundle.ProfileShareSafe, uuidA, "/home/dev/work")
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   "/home/dev/restored",
	}

	_, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != ErrRestoreNoNative {
		t.Fatalf("expected ErrRestoreNoNative for share-safe bundle, got %v", err)
	}
}

func TestRestore_TokenValidation(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mockTime := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	mgr, _ := newTestManager(t, roots, WithNow(func() time.Time { return mockTime }))

	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, "/home/dev/work")
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   "/home/dev/restored",
	}

	prev, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}

	// Test 1: Empty or corrupted token
	_, err = mgr.Restore(context.Background(), req, nil, "bad:token")
	if err != ErrPreviewStale {
		t.Errorf("expected ErrPreviewStale for corrupted token, got %v", err)
	}

	// Test 2: Expired token (advance time past tokenTTL of 5m).
	// WithNow captured mockTime by value, so move the manager clock directly.
	mgr.now = func() time.Time { return mockTime.Add(6 * time.Minute) }
	_, err = mgr.Restore(context.Background(), req, nil, prev.Token)
	if err != ErrPreviewStale {
		t.Errorf("expected ErrPreviewStale for expired token, got %v", err)
	}
	mgr.now = func() time.Time { return mockTime }

	// Test 3: Modified request (e.g. changed TargetCWD) stales token
	tamperedReq := req
	tamperedReq.TargetCWD = "/home/dev/other"
	_, err = mgr.Restore(context.Background(), tamperedReq, nil, prev.Token)
	if err != ErrPreviewStale {
		t.Errorf("expected ErrPreviewStale for tampered request, got %v", err)
	}
}

func TestRestore_SuccessfulClaudeRestore(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mgr, _ := newTestManager(t, roots)

	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	origCWD := "/home/dev/original"
	targetCWD := "/home/dev/new-workspace"

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, origCWD)
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   targetCWD,
	}

	prev, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked != "" {
		t.Fatalf("unexpected block: %s", prev.Items[0].Blocked)
	}
	if prev.Items[0].IsCopy {
		t.Fatalf("expected IsCopy = false for uncollided session")
	}

	// Execute restore
	report, err := mgr.Restore(context.Background(), req, nil, prev.Token)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if report.Restored != 1 {
		t.Fatalf("expected 1 restored session, got %d", report.Restored)
	}
	if report.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d", report.Failed)
	}
	if len(report.CreatedRefs) < 1 || report.CreatedRefs[0].ID != uuidA {
		t.Errorf("expected restored ref %s, got %+v", uuidA, report.CreatedRefs)
	}

	// Check files on disk
	encTarget := EncodeClaudeProjectDir(targetCWD)
	mainJSONL := filepath.Join(roots.Claude, "projects", encTarget, uuidA+".jsonl")
	subJSONL := filepath.Join(roots.Claude, "projects", encTarget, uuidA, "subagents", "agent-sub1.jsonl")
	subMeta := filepath.Join(roots.Claude, "projects", encTarget, uuidA, "subagents", "agent-sub1.meta.json")

	mainInfo, err := os.Stat(mainJSONL)
	if err != nil {
		t.Fatalf("stat main jsonl: %v", err)
	}
	if perm := mainInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}

	if _, err := os.Stat(subJSONL); err != nil {
		t.Fatalf("stat subagent jsonl: %v", err)
	}
	if _, err := os.Stat(subMeta); err != nil {
		t.Fatalf("stat subagent meta: %v", err)
	}

	// Verify cwd was rewritten in the JSONL
	content, err := os.ReadFile(mainJSONL)
	if err != nil {
		t.Fatalf("read main jsonl: %v", err)
	}
	if !strings.Contains(string(content), targetCWD) {
		t.Errorf("expected JSONL to contain target CWD %q, got:\n%s", targetCWD, string(content))
	}
	if strings.Contains(string(content), origCWD) {
		t.Errorf("expected JSONL not to contain original CWD %q", origCWD)
	}
}

func TestRestore_Collision_Block(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mgr, _ := newTestManager(t, roots)

	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, "/home/dev/work")
	catalog := []model.SessionMeta{
		claudeMainMeta(roots.Claude, uuidA),
	}

	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		Collision:   CollisionBlock, // default
	}

	prev, err := mgr.PreviewRestore(context.Background(), req, catalog)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked == "" {
		t.Errorf("expected collision block, but item was not blocked")
	}

	// Attempting to restore blocked item must fail
	_, err = mgr.Restore(context.Background(), req, catalog, prev.Token)
	if err == nil {
		t.Fatalf("expected error restoring blocked preview, got nil")
	}
}

func TestRestore_Collision_Copy(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mgr, _ := newTestManager(t, roots)

	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, "/home/dev/work")
	catalog := []model.SessionMeta{
		claudeMainMeta(roots.Claude, uuidA),
	}

	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   "/home/dev/restored",
		Collision:   CollisionCopy,
	}

	prev, err := mgr.PreviewRestore(context.Background(), req, catalog)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked != "" {
		t.Fatalf("expected item not to be blocked under copy mode, got: %s", prev.Items[0].Blocked)
	}
	if !prev.Items[0].IsCopy {
		t.Errorf("expected IsCopy = true")
	}
	if prev.Items[0].TargetRef.ID == uuidA {
		t.Errorf("expected new session ID for copy, got original %s", uuidA)
	}

	newID := prev.Items[0].TargetRef.ID
	req.CopyID = prev.CopyID

	// Execute restore
	report, err := mgr.Restore(context.Background(), req, catalog, prev.Token)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if report.Restored != 1 {
		t.Fatalf("expected 1 restored, got %d", report.Restored)
	}

	// Verify file created with newID
	encTarget := EncodeClaudeProjectDir("/home/dev/restored")
	mainJSONL := filepath.Join(roots.Claude, "projects", encTarget, newID+".jsonl")
	content, err := os.ReadFile(mainJSONL)
	if err != nil {
		t.Fatalf("read new session jsonl: %v", err)
	}
	if !strings.Contains(string(content), newID) {
		t.Errorf("expected JSONL to have new session ID %s, got:\n%s", newID, string(content))
	}
}

func TestRestore_LiveRefusal(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	liveFunc := func(_ context.Context, agent, id string) (bool, error) {
		if agent == string(model.AgentClaude) && id == uuidA {
			return true, nil // simulate live session
		}
		return false, nil
	}

	mgr, _ := newTestManager(t, roots, WithLiveFunc(liveFunc))
	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, "/home/dev/work")
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   "/home/dev/restored",
	}

	prev, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked == "" || !strings.Contains(prev.Items[0].Blocked, "active") {
		t.Errorf("expected live block, got: %s", prev.Items[0].Blocked)
	}
}

func TestRestore_NoOverwrite(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	mgr, _ := newTestManager(t, roots)

	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	targetCWD := "/home/dev/restored"
	encTarget := EncodeClaudeProjectDir(targetCWD)
	destDir := filepath.Join(roots.Claude, "projects", encTarget)
	_ = os.MkdirAll(destDir, 0o700)
	destFile := filepath.Join(destDir, uuidA+".jsonl")
	_ = os.WriteFile(destFile, []byte("pre-existing content"), 0o600)

	b := makeClaudeBundle(t, bundle.ProfileComplete, uuidA, "/home/dev/work")
	req := RestoreRequest{
		Bundle:      b,
		TargetAgent: model.AgentClaude,
		TargetCWD:   targetCWD,
		Collision:   CollisionBlock,
	}

	prev, err := mgr.PreviewRestore(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("PreviewRestore: %v", err)
	}
	if prev.Items[0].Blocked == "" {
		t.Errorf("expected block due to existing file on disk, got none")
	}
}

func TestRestore_ProtectedPath(t *testing.T) {
	roots := testRoots(t)
	prepareClaudeRoot(t, roots)
	// The destination project directory is derived from the target CWD, so a
	// protected name inside the bundle cannot survive that rewrite. Plant the
	// protected name as a real directory the restore would have to descend into.
	protected := filepath.Join(roots.Claude, "history.jsonl")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatalf("mkdir protected dir: %v", err)
	}
	mgr, _ := newTestManager(t, roots)
	cfg, _ := mgr.Config()
	cfg.AllowRestore = true
	_ = mgr.SetConfig(cfg)

	cm := mgr.claude
	dest := filepath.Join(protected, uuidA+".jsonl")
	if err := cm.ps.ensureWithinRoot(dest); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("expected protected path refusal, got %v", err)
	}
}
