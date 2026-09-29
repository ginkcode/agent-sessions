package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	scancat "github.com/ginkcode/agent-sessions/internal/scan"
)

func sampleBundleBytes(t *testing.T, profile bundle.Profile) []byte {
	t.Helper()
	var native []bundle.NativeInput
	if profile == bundle.ProfileComplete {
		native = []bundle.NativeInput{
			{Agent: "claude-code", Rel: "projects/-home-dev/session.jsonl", Data: []byte(`{"cwd":"/home/dev/work/project"}` + "\n")},
		}
	}
	req := bundle.WriteRequest{
		CreatedAt:  time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
		AppVersion: "0.6.0",
		Profile:    profile,
		Source: bundle.SourceMeta{
			Agent:     "claude-code",
			ID:        "11111111-2222-3333-4444-555555555555",
			CWD:       "/home/dev/work/project",
			GitBranch: "main",
			Title:     "Imported Session",
		},
		Sessions: []bundle.SessionInput{
			{
				Agent:  "claude-code",
				ID:     "11111111-2222-3333-4444-555555555555",
				Native: native,
			},
		},
		Redaction: bundle.RedactionManifest{Rules: map[string]int{"token": 1}, Counts: 1},
		Handoff:   "# Handoff\n\nTask context\n",
		Transcript: []byte(`[{
			"meta": {
				"ref": {"agent": "claude-code", "id": "11111111-2222-3333-4444-555555555555"},
				"cwd": "/home/dev/work/project",
				"title": "Imported Session"
			},
			"messages": [
				{"role": "user", "parts": [{"kind": "text", "text": "Hello agent"}]},
				{"role": "assistant", "parts": [{"kind": "text", "text": "I can help with that."}]}
			]
		}]`),
	}
	var buf bytes.Buffer
	if err := bundle.Write(&buf, req); err != nil {
		t.Fatalf("bundle.Write: %v", err)
	}
	return buf.Bytes()
}

func TestOpenBundleBytes_Complete(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	data := sampleBundleBytes(t, bundle.ProfileComplete)

	summary, err := svc.OpenBundleBytes(context.Background(), "test.agent-session.zip", data)
	if err != nil {
		t.Fatalf("OpenBundleBytes: %v", err)
	}

	if summary.BundleID == "" {
		t.Errorf("expected non-empty BundleID")
	}
	if summary.Profile != "complete" {
		t.Errorf("expected profile complete, got %q", summary.Profile)
	}
	if !summary.Verified {
		t.Errorf("expected verified = true")
	}
	if !summary.RestoreAvailable {
		t.Errorf("expected restoreAvailable = true for complete profile")
	}
	if !summary.HandoffAvailable {
		t.Errorf("expected handoffAvailable = true")
	}
	if summary.SessionsCount != 1 {
		t.Errorf("expected 1 session, got %d", summary.SessionsCount)
	}
	if summary.NativeFilesCount != 1 {
		t.Errorf("expected 1 native file, got %d", summary.NativeFilesCount)
	}
	if len(summary.Sessions) != 1 || summary.Sessions[0].Title != "Imported Session" {
		t.Errorf("expected session title 'Imported Session', got %+v", summary.Sessions)
	}
}

func TestOpenBundleBytes_ShareSafe(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	data := sampleBundleBytes(t, bundle.ProfileShareSafe)

	summary, err := svc.OpenBundleBytes(context.Background(), "safe.agent-session.zip", data)
	if err != nil {
		t.Fatalf("OpenBundleBytes: %v", err)
	}

	if summary.Profile != "share-safe" {
		t.Errorf("expected profile share-safe, got %q", summary.Profile)
	}
	if summary.RestoreAvailable {
		t.Errorf("expected restoreAvailable = false for share-safe profile")
	}
	if !summary.HandoffAvailable {
		t.Errorf("expected handoffAvailable = true")
	}
	if summary.NativeFilesCount != 0 {
		t.Errorf("expected 0 native files for share-safe, got %d", summary.NativeFilesCount)
	}
}

func TestOpenBundle_FileOnDisk(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	dir := t.TempDir()
	path := filepath.Join(dir, "session.agent-session.zip")
	data := sampleBundleBytes(t, bundle.ProfileComplete)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	summary, err := svc.OpenBundle(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenBundle: %v", err)
	}
	if summary.Path != path {
		t.Errorf("expected path %s, got %s", path, summary.Path)
	}
	if summary.Source.Title != "Imported Session" {
		t.Errorf("expected title 'Imported Session', got %q", summary.Source.Title)
	}
}

func TestBuildBundleHandoff(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	data := sampleBundleBytes(t, bundle.ProfileComplete)
	summary, err := svc.OpenBundleBytes(context.Background(), "test.agent-session.zip", data)
	if err != nil {
		t.Fatalf("OpenBundleBytes: %v", err)
	}

	preview, err := svc.BuildBundleHandoff(context.Background(), BundleHandoffRequest{
		BundleID: summary.BundleID,
		Target:   model.AgentCodex,
		Budget:   80000,
	})
	if err != nil {
		t.Fatalf("BuildBundleHandoff: %v", err)
	}

	if !strings.Contains(preview.PromptMarkdown, "Hello agent") {
		t.Errorf("expected PromptMarkdown to contain 'Hello agent', got:\n%s", preview.PromptMarkdown)
	}
	if !strings.Contains(preview.Command, "codex") {
		t.Errorf("expected command to invoke codex, got: %s", preview.Command)
	}
}

func TestSaveBundleHandoff(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	data := sampleBundleBytes(t, bundle.ProfileComplete)
	summary, err := svc.OpenBundleBytes(context.Background(), "test.agent-session.zip", data)
	if err != nil {
		t.Fatalf("OpenBundleBytes: %v", err)
	}

	outDir := t.TempDir()
	destPath := filepath.Join(outDir, "handoff.md")

	saved, err := svc.SaveBundleHandoff(context.Background(), BundleHandoffRequest{
		BundleID: summary.BundleID,
		Target:   model.AgentOpenCode,
	}, destPath)
	if err != nil {
		t.Fatalf("SaveBundleHandoff: %v", err)
	}
	if saved != destPath {
		t.Errorf("expected %s, got %s", destPath, saved)
	}

	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(content), "Hello agent") {
		t.Errorf("saved file missing prompt content: %s", string(content))
	}
}

func TestOpenBundle_CorruptFileRejected(t *testing.T) {
	svc := NewService(scancat.NewCatalog(), provider.Set{})
	_, err := svc.OpenBundleBytes(context.Background(), "bad.zip", []byte("not a zip file"))
	if err == nil {
		t.Fatalf("expected error on corrupt file, got nil")
	}
}
