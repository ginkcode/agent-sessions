package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func setupHandoffTest(t *testing.T) (*App, *Service, *providertest.Fake) {
	t.Helper()
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	providers := provider.Set{fake}
	catalog := scan.NewCatalog()
	svc := NewService(catalog, providers)
	tempData := t.TempDir()
	svc.SetDataDir(tempData)

	now := time.Now()
	rootMeta := model.SessionMeta{
		Ref:         model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Title:       "Test Session",
		CWD:         "/tmp/work",
		FirstPrompt: "Initial prompt",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	catalog.Apply(provider.ScanResult{Changed: []model.SessionMeta{rootMeta}})
	fake.Sessions = []model.SessionMeta{rootMeta}
	fake.Transcripts["sess-root"] = &model.Transcript{
		Meta: rootMeta,
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "Implement feature X"},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "Working on feature X"},
				},
			},
		},
	}

	app := NewAppWithService(svc)
	return app, svc, fake
}

func TestBuildHandoff(t *testing.T) {
	app, _, _ := setupHandoffTest(t)

	req := HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: model.AgentCodex,
		Budget: 80000,
	}

	preview, err := app.BuildHandoff(req)
	if err != nil {
		t.Fatalf("BuildHandoff failed: %v", err)
	}

	if preview.PromptMarkdown == "" {
		t.Error("expected non-empty PromptMarkdown")
	}
	if preview.FullMarkdown == "" {
		t.Error("expected non-empty FullMarkdown")
	}
	if preview.Report.EstimatedTokens == 0 {
		t.Error("expected non-zero EstimatedTokens")
	}
	if !strings.Contains(preview.Command, "codex") {
		t.Errorf("expected codex in command: %s", preview.Command)
	}
	if !strings.Contains(preview.Command, "cd /tmp/work") {
		t.Errorf("expected cwd in command: %s", preview.Command)
	}
	if !strings.Contains(preview.Command, "Read "+preview.PromptFile+" completely") {
		t.Errorf("command should point at the prompt file %s: %s", preview.PromptFile, preview.Command)
	}

	// Verify no file was written to disk by BuildHandoff
	for _, f := range []string{preview.ContextFile, preview.PromptFile} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("BuildHandoff should not write %s to disk", f)
		}
	}
}

func TestBuildHandoffValidation(t *testing.T) {
	app, _, _ := setupHandoffTest(t)

	// Unknown session
	_, err := app.BuildHandoff(HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "non-existent"},
		Target: model.AgentCodex,
	})
	if err == nil {
		t.Error("expected error for unknown session")
	}

	// Unknown target agent
	_, err = app.BuildHandoff(HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: "unknown-agent",
	})
	if err == nil {
		t.Error("expected error for unknown target agent")
	}

	// Relative CWD
	_, err = app.BuildHandoff(HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: model.AgentCodex,
		CWD:    "relative/path",
	})
	if err == nil {
		t.Error("expected error for relative CWD")
	}
}

func TestHandoffCommand(t *testing.T) {
	app, svc, _ := setupHandoffTest(t)

	req := HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: model.AgentOpenCode,
		CWD:    "/custom/dir",
	}

	cmd, err := app.HandoffCommand(req)
	if err != nil {
		t.Fatalf("HandoffCommand failed: %v", err)
	}

	if !strings.Contains(cmd, "cd /custom/dir") {
		t.Errorf("expected custom cwd in command: %s", cmd)
	}
	if !strings.Contains(cmd, "opencode --prompt") {
		t.Errorf("expected opencode --prompt in command: %s", cmd)
	}

	// Verify both files WERE written with 0600 mode
	ctxFile, _ := handoff.ContextFilePath(svc.DataDir(), "sess-root")
	promptFile, _ := handoff.PromptFilePath(svc.DataDir(), "sess-root")
	for _, f := range []string{ctxFile, promptFile} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatalf("handoff file was not written: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("expected 0600 file mode for %s, got %o", f, fi.Mode().Perm())
		}
	}
	if !strings.Contains(cmd, "Read "+promptFile+" completely") {
		t.Errorf("command should point at the prompt file: %s", cmd)
	}
	prompt, _ := os.ReadFile(promptFile)
	if !strings.Contains(string(prompt), ctxFile) {
		t.Error("prompt file should reference the full context file")
	}

	info := svc.HandoffCache()
	if info.Files != 2 || info.Bytes == 0 {
		t.Errorf("cache info = %+v", info)
	}
	info, err = svc.ClearHandoffCache()
	if err != nil || info.Files != 0 {
		t.Errorf("clear = %+v, %v", info, err)
	}
}

func TestSaveHandoff(t *testing.T) {
	app, _, _ := setupHandoffTest(t)

	tempDir := t.TempDir()
	saveTarget := filepath.Join(tempDir, "export-test.md")

	// Set override dialog
	app.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		if !strings.HasSuffix(defaultName, "sess-root-handoff.md") {
			t.Errorf("unexpected default filename: %s", defaultName)
		}
		return saveTarget, nil
	}

	req := HandoffRequest{
		Ref:    model.SessionRef{Agent: model.AgentClaude, ID: "sess-root"},
		Target: model.AgentCodex,
	}

	path, err := app.SaveHandoff(req)
	if err != nil {
		t.Fatalf("SaveHandoff failed: %v", err)
	}
	if path != saveTarget {
		t.Errorf("got path %s, want %s", path, saveTarget)
	}

	content, err := os.ReadFile(saveTarget)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(content), "Initial prompt") && !strings.Contains(string(content), "feature X") {
		t.Errorf("content missing session info: %s", string(content))
	}

	fi, err := os.Stat(saveTarget)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 mode, got %o", fi.Mode().Perm())
	}

	// Test cancellation
	app.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		return "", nil // user cancelled
	}
	path, err = app.SaveHandoff(req)
	if err != nil {
		t.Fatalf("SaveHandoff cancel failed: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path on cancel, got %s", path)
	}
}
