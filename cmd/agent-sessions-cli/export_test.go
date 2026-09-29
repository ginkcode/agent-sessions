package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func TestExportThenInspect(t *testing.T) {
	id := "11111111-2222-3333-4444-555555555555"
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	meta := model.SessionMeta{
		Ref:   model.SessionRef{Agent: model.AgentClaude, ID: id},
		Title: "CLI export",
		CWD:   "/work/cli",
	}
	fake.Sessions = []model.SessionMeta{meta}
	fake.Transcripts[id] = &model.Transcript{
		Meta: meta,
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "do the cli export"}}},
		},
	}
	dest := filepath.Join(t.TempDir(), "session.agent-session.zip")

	code, out, diag := runFake([]string{"export", "claude-code", id, "--profile", "share-safe", "-o", dest}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("export exit %d: %s", code, diag)
	}
	if strings.TrimSpace(out) != dest {
		t.Fatalf("export output = %q", out)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("bundle not written: %v", err)
	}

	code, out, diag = runFake([]string{"inspect", dest}, nil)
	if code != 0 {
		t.Fatalf("inspect exit %d: %s", code, diag)
	}
	for _, want := range []string{"profile:  share-safe", "source:   claude-code " + id, "title:    CLI export", "sessions: 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output missing %q:\n%s", want, out)
		}
	}

	code, out, diag = runFake([]string{"inspect", dest, "--json"}, nil)
	if code != 0 {
		t.Fatalf("inspect --json exit %d: %s", code, diag)
	}
	if !strings.Contains(out, `"profile": "share-safe"`) || !strings.Contains(out, `"id": "`+id+`"`) {
		t.Fatalf("inspect json = %s", out)
	}
}

func TestExportRequiresOutput(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	code, _, _ := runFake([]string{"export", "claude-code", "s1"}, provider.Set{fake})
	if code != 2 {
		t.Fatalf("export without -o exit = %d, want 2", code)
	}
}

func TestInspectRejectsGarbage(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "nope.zip")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, diag := runFake([]string{"inspect", bad}, nil)
	if code != 1 {
		t.Fatalf("inspect garbage exit = %d (%s), want 1", code, diag)
	}
}
