package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

const testClaudeID = "0123abcd-0000-4000-8000-000000000001"

// fakeTrash records trash calls without touching the disk.
type fakeTrash struct {
	calls []string
	fail  map[string]error
}

func (f *fakeTrash) Trash(path string) error {
	if err := f.fail[path]; err != nil {
		return err
	}
	f.calls = append(f.calls, path)
	return nil
}

func (f *fakeTrash) Name() string { return "fake" }

// noProcs is a process table with no agent processes running.
type noProcs struct{}

func (noProcs) Cmdlines() (map[int][]string, error) { return map[int][]string{}, nil }

type manageFixture struct {
	app    *App
	trash  *fakeTrash
	ref    model.SessionRef
	source string
}

// newManageTestApp builds an App over a temp Claude root holding one old
// session, so destructive tests never see the user's real stores.
func newManageTestApp(t *testing.T, live manage.LiveFunc) manageFixture {
	t.Helper()

	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	projectDir := filepath.Join(claudeRoot, "projects", "p")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(projectDir, testClaudeID+".jsonl")
	if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}

	ref := model.SessionRef{Agent: model.AgentClaude, ID: testClaudeID}
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.DetectionData = provider.Detection{Present: true}
	fake.Sessions = []model.SessionMeta{{
		Ref:        ref,
		Title:      "Old session",
		SourcePath: source,
		UpdatedAt:  old,
	}}

	catalog := scan.NewCatalog()
	svc := NewService(catalog, provider.Set{fake})
	a := NewAppWithService(svc)
	a.cacheDirOverride = t.TempDir()
	catalog.Apply(provider.ScanResult{Changed: fake.Sessions})

	trash := &fakeTrash{fail: make(map[string]error)}
	opts := []manage.Option{manage.WithTrash(trash), manage.WithProcFS(noProcs{})}
	if live != nil {
		opts = append(opts, manage.WithLiveFunc(live))
	}
	mgr, err := manage.New(
		paths.Roots{Claude: claudeRoot},
		filepath.Join(root, "config", "config.toml"),
		opts...,
	)
	if err != nil {
		t.Fatalf("manage.New: %v", err)
	}
	a.manageOverride = mgr
	return manageFixture{app: a, trash: trash, ref: ref, source: source}
}

func notLive(context.Context, string, string) (bool, error) { return false, nil }

func TestPreviewDelete_GuardDisabled(t *testing.T) {
	f := newManageTestApp(t, notLive)

	// Config starts disabled by default.
	_, err := f.app.PreviewDelete([]model.SessionRef{f.ref})
	if !errors.Is(err, ErrManageDisabled) {
		t.Errorf("PreviewDelete with disabled manage = %v, want ErrManageDisabled", err)
	}
}

func TestDeleteSessions_Guards(t *testing.T) {
	f := newManageTestApp(t, notLive)
	refs := []model.SessionRef{f.ref}

	if _, err := f.app.DeleteSessions(nil, "tok"); err == nil {
		t.Error("DeleteSessions(no refs) succeeded")
	}
	if _, err := f.app.DeleteSessions(refs, ""); !errors.Is(err, ErrPreviewStale) {
		t.Errorf("DeleteSessions(no token) = %v, want ErrPreviewStale", err)
	}
	if _, err := f.app.DeleteSessions(refs, "1:abcd"); !errors.Is(err, ErrManageDisabled) {
		t.Errorf("DeleteSessions(disabled) = %v, want ErrManageDisabled", err)
	}

	if _, err := f.app.SetManageEnabled(true); err != nil {
		t.Fatalf("SetManageEnabled: %v", err)
	}
	unknown := []model.SessionRef{{Agent: model.AgentClaude, ID: "ffffffff-0000-4000-8000-000000000000"}}
	if _, err := f.app.DeleteSessions(unknown, "1:abcd"); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("DeleteSessions(unknown) = %v, want ErrUnknownSession", err)
	}
	if _, err := f.app.DeleteSessions(refs, "1:abcd"); !errors.Is(err, ErrPreviewStale) {
		t.Errorf("DeleteSessions(forged token) = %v, want ErrPreviewStale", err)
	}

	if len(f.trash.calls) != 0 {
		t.Errorf("guard failure trashed %v", f.trash.calls)
	}
	if _, ok := f.app.svc.Catalog().Get(f.ref); !ok {
		t.Error("guard failure removed session from catalog")
	}
}

func TestDeleteSessions_TrashesAndForgets(t *testing.T) {
	f := newManageTestApp(t, notLive)
	if _, err := f.app.SetManageEnabled(true); err != nil {
		t.Fatalf("SetManageEnabled: %v", err)
	}
	refs := []model.SessionRef{f.ref}

	p, err := f.app.PreviewDelete(refs)
	if err != nil {
		t.Fatalf("PreviewDelete: %v", err)
	}
	if len(p.Items) != 1 || p.Items[0].Blocked != "" || !p.Items[0].Reversible || p.Token == "" {
		t.Fatalf("preview = %+v", p)
	}

	rep, err := f.app.DeleteSessions(refs, p.Token)
	if err != nil {
		t.Fatalf("DeleteSessions: %v", err)
	}
	if rep.Deleted != 1 || rep.Failed != 0 {
		t.Errorf("report = %+v", rep)
	}
	if len(f.trash.calls) != 1 || f.trash.calls[0] != f.source {
		t.Errorf("trash calls = %v, want [%s]", f.trash.calls, f.source)
	}
	if _, ok := f.app.svc.Catalog().Get(f.ref); ok {
		t.Error("deleted session still in catalog")
	}
}

func TestPreviewDelete_LiveBlocks(t *testing.T) {
	cases := map[string]manage.LiveFunc{
		"live": func(context.Context, string, string) (bool, error) { return true, nil },
		"unknown": func(context.Context, string, string) (bool, error) {
			return false, errors.New("detector failed")
		},
		"no detector": nil,
	}
	for name, live := range cases {
		t.Run(name, func(t *testing.T) {
			f := newManageTestApp(t, live)
			if _, err := f.app.SetManageEnabled(true); err != nil {
				t.Fatalf("SetManageEnabled: %v", err)
			}
			p, err := f.app.PreviewDelete([]model.SessionRef{f.ref})
			if err != nil {
				t.Fatalf("PreviewDelete: %v", err)
			}
			if len(p.Items) != 1 || p.Items[0].Blocked == "" {
				t.Errorf("session not blocked: %+v", p.Items)
			}
		})
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	f := newManageTestApp(t, notLive)
	s, err := f.app.GetSettings()
	if err != nil || s.Enabled || s.AllowPermanentDelete {
		t.Fatalf("default settings = %+v, %v", s, err)
	}
	if _, err := f.app.SetManageEnabled(true); err != nil {
		t.Fatal(err)
	}
	if s, err = f.app.SetAllowPermanentDelete(true); err != nil || !s.Enabled || !s.AllowPermanentDelete {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	if s, err = f.app.GetSettings(); err != nil || !s.Enabled || !s.AllowPermanentDelete {
		t.Fatalf("reloaded settings = %+v, %v", s, err)
	}
}

func TestForget_CatalogEviction(t *testing.T) {
	f := newManageTestApp(t, notLive)
	f.app.forget([]model.SessionRef{f.ref})
	if _, ok := f.app.svc.Catalog().Get(f.ref); ok {
		t.Error("forget did not evict the session from the catalog")
	}
}

func TestLRUEvict(t *testing.T) {
	c := newLRU[string, int](2)
	c.put("a", 1)
	c.put("b", 2)
	c.evict("a")
	if _, ok := c.get("a"); ok {
		t.Error("evicted key still present")
	}
	if _, ok := c.get("b"); !ok {
		t.Error("other key lost")
	}
}

func TestManageConfigFilePerms(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	store := manage.NewConfigStore(cfgPath)

	if err := store.Save(manage.Config{Enabled: true, AllowPermanentDelete: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Enabled || !cfg.AllowPermanentDelete {
		t.Errorf("round-trip lost settings: %+v", cfg)
	}

	// A config that governs deletes must not be group/other readable.
	fi, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if platform.ModeBits && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("config perms %v leak to group/other", fi.Mode().Perm())
	}
}
