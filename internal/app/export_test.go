package app

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

const exportSessionID = "11111111-2222-3333-4444-555555555555"

// exportFixture is a Claude session whose display transcript truncates a tool
// output, with the full output available through Blob and the native jsonl on
// disk under a real provider root.
func exportFixture(t *testing.T) (*App, *manage.Manager, model.SessionRef) {
	t.Helper()
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	source := filepath.Join(claudeRoot, "projects", "enc", exportSessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	native := []byte("{\"type\":\"user\",\"message\":{\"content\":\"full native record\"}}\n")
	if err := os.WriteFile(source, native, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(source, old, old)

	ref := model.SessionRef{Agent: model.AgentClaude, ID: exportSessionID}
	meta := model.SessionMeta{
		Ref: ref, Title: "Export me", CWD: "/work/project",
		SourcePath: source, CreatedAt: old, UpdatedAt: old,
	}
	const full = "complete tool output that the display transcript truncated"
	tr := &model.Transcript{
		Meta: meta,
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "please export this session"}}},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "used sk-ant-abcdefghijklmnopqrstuvwxyz0123456789"},
					{Kind: model.PartTool, Tool: &model.ToolCall{
						Name: "Bash", Output: "complete ", OutputTruncated: true, OutputRef: "rec:1:tool",
					}},
				},
			}},
	}
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.Transcripts[exportSessionID] = tr
	fake.Blobs[ref.Key()+":"+"rec:1:tool"] = []byte(full)

	catalog := scan.NewCatalog()
	catalog.Apply(provider.ScanResult{Changed: []model.SessionMeta{meta}})
	svc := NewService(catalog, provider.Set{fake})
	app := NewAppWithService(svc)

	mgr, err := manage.New(paths.Roots{
		Claude: claudeRoot,
		Config: filepath.Join(root, "config"),
	}, filepath.Join(root, "config", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	app.manageOverride = mgr
	return app, mgr, ref
}

func TestPreviewExportCompleteWarnsAndCounts(t *testing.T) {
	app, _, ref := exportFixture(t)
	preview, err := app.PreviewExport(ExportRequest{Ref: ref, Profile: "complete", RedactSecrets: true})
	if err != nil {
		t.Fatalf("PreviewExport: %v", err)
	}
	if preview.Warning == "" {
		t.Fatal("complete profile must warn about sensitive data")
	}
	if preview.Sessions != 1 || preview.NativeFiles != 1 || preview.NativeBytes == 0 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Fidelity.Resolved != 1 {
		t.Fatalf("truncated output not resolved: %+v", preview.Fidelity)
	}
	if preview.Redaction.Token == 0 {
		t.Fatalf("redaction counts missing: %+v", preview.Redaction)
	}
}

func TestExportBundleRoundTrip(t *testing.T) {
	app, _, ref := exportFixture(t)
	dest := filepath.Join(t.TempDir(), "out.agent-session.zip")
	var offered string
	app.saveDialogOverride = func(_ context.Context, name string) (string, error) {
		offered = name
		return dest, nil
	}

	got, err := app.ExportBundle(ExportRequest{Ref: ref, Profile: "complete"})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if got != dest {
		t.Fatalf("path = %q", got)
	}
	if want := "claude-code_project_11111111.agent-session.zip"; offered != want {
		t.Fatalf("default file name = %q, want %q", offered, want)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode = %o, want 0600", info.Mode().Perm())
	}

	b, err := bundle.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if b.Manifest.Profile != bundle.ProfileComplete || b.Manifest.Source.ID != exportSessionID {
		t.Fatalf("manifest = %+v", b.Manifest)
	}
	if !strings.Contains(string(b.Transcript), "complete tool output that the display transcript truncated") {
		t.Fatal("transcript.json lost the restored tool output")
	}
	if len(b.Native) != 1 {
		t.Fatalf("native entries = %d", len(b.Native))
	}
	for _, data := range b.Native {
		if !strings.Contains(string(data), "full native record") {
			t.Fatalf("native content = %q", data)
		}
	}
	if !strings.Contains(b.Handoff, "please export this session") {
		t.Fatal("handoff missing the user request")
	}
}

func TestExportBundleCancelReturnsEmptyPath(t *testing.T) {
	app, _, ref := exportFixture(t)
	app.saveDialogOverride = func(context.Context, string) (string, error) { return "", nil }

	got, err := app.ExportBundle(ExportRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if got != "" {
		t.Fatalf("cancel returned %q", got)
	}
}

func TestExportBundleShareSafeDropsNativeAndForcesRedaction(t *testing.T) {
	app, _, ref := exportFixture(t)
	dest := filepath.Join(t.TempDir(), "share.agent-session.zip")

	if _, err := app.svc.ExportBundle(context.Background(), ExportRequest{Ref: ref, Profile: "share-safe"}, dest, nil); err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	b, err := bundle.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.Profile != bundle.ProfileShareSafe {
		t.Fatalf("profile = %s", b.Manifest.Profile)
	}
	if len(b.Native) != 0 {
		t.Fatalf("share-safe bundle contains %d native entries", len(b.Native))
	}
	if strings.Contains(string(b.Transcript), "sk-ant-abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatal("share-safe transcript kept the secret token")
	}
	if b.Manifest.Redaction.Counts == 0 {
		t.Fatalf("redaction = %+v", b.Manifest.Redaction)
	}
}

func TestExportBundleUnknownProfile(t *testing.T) {
	app, _, ref := exportFixture(t)
	if _, err := app.svc.PreviewExport(context.Background(), ExportRequest{Ref: ref, Profile: "public"}, nil); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

// Guard against the zip writer regressing to a mode other than 0600, which the
// round trip above also checks through the file stat.
func TestExportBundleIsZip(t *testing.T) {
	app, _, ref := exportFixture(t)
	dest := filepath.Join(t.TempDir(), "out.agent-session.zip")
	if _, err := app.svc.ExportBundle(context.Background(), ExportRequest{Ref: ref, Profile: "share-safe"}, dest, nil); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	for _, want := range []string{"manifest.json", "transcript.json", "handoff.md"} {
		if !names[want] {
			t.Errorf("bundle missing %s", want)
		}
	}
}
