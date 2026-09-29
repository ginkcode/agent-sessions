package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func runFake(args []string, providers provider.Set) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, providers, &out, &errOut)
	return code, out.String(), errOut.String()
}

func testMeta(id string, updated time.Time, parent string) model.SessionMeta {
	return model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: id},
		ParentID:  parent,
		CWD:       "/tmp/project",
		Title:     "Title " + id,
		UpdatedAt: updated,
		Counts:    model.MessageCounts{User: 2, Assistant: 3, ToolCalls: 4},
	}
}

func TestScanTextOrderingAndSubagentFilter(t *testing.T) {
	now := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.Sessions = []model.SessionMeta{
		testMeta("older", now.Add(-time.Hour), ""),
		testMeta("parent/agent-child", now.Add(time.Hour), "parent"),
		testMeta("newer", now, ""),
	}

	code, out, diag := runFake([]string{"scan", "--agent", "claude-code"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, diag)
	}
	if !strings.Contains(out, "AGENT") || !strings.Contains(out, "ID") || !strings.Contains(out, "UPDATED") || !strings.Contains(out, "MSGS") || !strings.Contains(out, "CWD") || !strings.Contains(out, "TITLE") {
		t.Errorf("missing scan column: %q", out)
	}
	if !strings.Contains(out, "claude-code  newer") {
		t.Errorf("scan row missing session ID: %q", out)
	}
	if strings.Contains(out, "agent-child") {
		t.Errorf("subagent listed without --all: %q", out)
	}
	if posNew, posOld := strings.Index(out, "Title newer"), strings.Index(out, "Title older"); posNew < 0 || posOld < 0 || posNew >= posOld {
		t.Errorf("not sorted newest first: %q", out)
	}
	if !strings.Contains(out, "5") {
		t.Errorf("expected user+assistant count: %q", out)
	}
	if !strings.Contains(diag, "claude-code:") || !strings.Contains(diag, "0 parse errors") {
		t.Errorf("missing diagnostics on stderr: %q", diag)
	}
}

func TestScanJSONAndAll(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.Sessions = []model.SessionMeta{
		testMeta("parent", time.Unix(10, 0), ""),
		testMeta("parent/agent-child", time.Unix(20, 0), "parent"),
	}
	code, out, diag := runFake([]string{"scan", "--json", "--all"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, diag)
	}
	var sessions []model.SessionMeta
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, out)
	}
	if len(sessions) != 2 || sessions[0].Ref.ID != "parent/agent-child" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	if !strings.HasPrefix(out, "[\n  {") {
		t.Errorf("JSON not indented: %q", out)
	}
}

func TestScanAcrossProvidersAndErrors(t *testing.T) {
	first := providertest.NewFake(model.AgentClaude, "Claude Code")
	second := providertest.NewFake(model.AgentCodex, "Codex")
	second.Sessions = []model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentCodex, ID: "codex-1"}, Title: "Codex title"}}
	providers := provider.Set{first, second}
	code, out, diag := runFake([]string{"scan", "--agent", "codex"}, providers)
	if code != 0 || !strings.Contains(out, "Codex title") || strings.Contains(diag, "claude-code:") {
		t.Fatalf("targeted scan: exit=%d stdout=%q stderr=%q", code, out, diag)
	}

	first.ScanErr = errors.New("no access")
	code, _, diag = runFake([]string{"scan"}, providers)
	if code != 1 || !strings.Contains(diag, "no access") {
		t.Errorf("scan error: exit=%d stderr=%q", code, diag)
	}
}

func TestShowTextMetaAndTruncation(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.Transcripts["s1"] = &model.Transcript{Messages: []model.Message{
		{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "hello"}}},
		{Role: model.RoleAssistant, Model: "model-x", Parts: []model.Part{
			{Kind: model.PartText, Text: "answer"},
			{Kind: model.PartReasoning, Text: "thought"},
			{Kind: model.PartTool, Tool: &model.ToolCall{Name: "bash", Status: model.ToolCompleted, Input: json.RawMessage(`{"cmd":"ls"}`), Output: "éabc"}},
		}},
		{Role: model.RoleSystem, IsMeta: true, Parts: []model.Part{{Kind: model.PartNotice, Text: "hidden notice"}}},
		{Role: model.RoleSystem, Parts: []model.Part{{Kind: model.PartCompaction, Text: "summary"}}},
	}}
	code, out, diag := runFake([]string{"show", "claude-code", "s1", "--max-output", "2"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, diag)
	}
	for _, text := range []string{"▶ user", "hello", "◀ assistant (model-x)", "[reasoning thought]", "[tool bash ✓]", "éa…", "── compacted ──"} {
		if !strings.Contains(out, text) {
			t.Errorf("output missing %q: %q", text, out)
		}
	}
	if strings.Contains(out, "hidden notice") {
		t.Errorf("meta notice not hidden: %q", out)
	}
	code, out, diag = runFake([]string{"show", "--meta", "claude-code", "s1"}, provider.Set{fake})
	if code != 0 || !strings.Contains(out, "hidden notice") {
		t.Errorf("show --meta: exit=%d stdout=%q stderr=%q", code, out, diag)
	}
}

func TestShowJSONFiltersMeta(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.Transcripts["s1"] = &model.Transcript{
		Meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "s1"}},
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "shown"}}},
			{Role: model.RoleUser, IsMeta: true, Parts: []model.Part{{Kind: model.PartText, Text: "hidden"}}},
		},
	}
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"default", []string{"show", "claude-code", "s1", "--json"}, 1},
		{"with-meta", []string{"show", "claude-code", "s1", "--meta", "--json"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, diag := runFake(tc.args, provider.Set{fake})
			if code != 0 {
				t.Fatalf("exit %d; stderr=%s", code, diag)
			}
			var tr model.Transcript
			if err := json.Unmarshal([]byte(out), &tr); err != nil {
				t.Fatalf("bad JSON: %v (%q)", err, out)
			}
			if len(tr.Messages) != tc.want || tr.Meta.Ref.ID != "s1" {
				t.Errorf("unexpected transcript: %+v", tr)
			}
		})
	}
}

func TestShowOutputLimitAndWriteErrors(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.Transcripts["s1"] = &model.Transcript{Messages: []model.Message{{
		Role: model.RoleAssistant,
		Parts: []model.Part{{Kind: model.PartTool, Tool: &model.ToolCall{
			Name: "bash", Status: model.ToolCompleted, Output: "long", OutputTruncated: true,
		}}},
	}}}
	code, out, diag := runFake([]string{"show", "claude-code", "s1", "--max-output=2"}, provider.Set{fake})
	if code != 0 || !strings.Contains(out, "lo…") || strings.Contains(out, "lo……") {
		t.Errorf("truncation: exit=%d stdout=%q stderr=%q", code, out, diag)
	}

	var stderr bytes.Buffer
	code = run(context.Background(), []string{"scan", "--json"}, provider.Set{fake}, failingWriter{}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "encode JSON") {
		t.Errorf("JSON write failure: exit=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	code = run(context.Background(), []string{"show", "claude-code", "s1"}, provider.Set{fake}, failingWriter{}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "write output") {
		t.Errorf("show write failure: exit=%d stderr=%q", code, stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestDetect(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	fake.DetectionData = provider.Detection{Present: true, Roots: []string{"/tmp/claude"}}
	code, out, diag := runFake([]string{"detect"}, provider.Set{fake})
	if code != 0 || !strings.Contains(out, "claude-code") || !strings.Contains(out, "true") || !strings.Contains(out, "/tmp/claude") || diag != "" {
		t.Errorf("detect: exit=%d stdout=%q stderr=%q", code, out, diag)
	}
	fake.DetectErr = errors.New("unavailable")
	code, _, diag = runFake([]string{"detect"}, provider.Set{fake})
	if code != 1 || !strings.Contains(diag, "unavailable") {
		t.Errorf("detect error: exit=%d stderr=%q", code, diag)
	}
}

func TestExitCodes(t *testing.T) {
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"empty", nil, 2},
		{"unknown command", []string{"bogus"}, 2},
		{"unknown agent", []string{"scan", "--agent", "bogus"}, 2},
		{"unknown show agent", []string{"show", "bogus", "id"}, 2},
		{"missing show id", []string{"show", "claude-code"}, 2},
		{"invalid max output", []string{"show", "claude-code", "id", "--max-output", "-1"}, 2},
		{"unknown scan flag", []string{"scan", "--bogus"}, 2},
		{"unknown show flag", []string{"show", "claude-code", "id", "--bogus"}, 2},
		{"detect extra argument", []string{"detect", "extra"}, 2},
		{"missing transcript", []string{"show", "claude-code", "missing"}, 1},
		{"missing handoff target", []string{"handoff", "claude-code", "s1"}, 2},
		{"unknown handoff target", []string{"handoff", "claude-code", "s1", "--target", "unknown"}, 2},
		{"invalid handoff budget", []string{"handoff", "claude-code", "s1", "--target", "codex", "--budget", "bad"}, 2},
		{"relative handoff cwd", []string{"handoff", "claude-code", "s1", "--target", "codex", "--cwd", "relative/path"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, _ := runFake(tc.args, provider.Set{fake})
			if code != tc.want {
				t.Errorf("exit=%d, want=%d", code, tc.want)
			}
		})
	}
}

func TestHandoffCLI(t *testing.T) {
	// The handoff files go to the data dir; keep them out of the real one.
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	now := time.Now()
	rootMeta := model.SessionMeta{
		Ref:         model.SessionRef{Agent: model.AgentClaude, ID: "s1"},
		Title:       "Root Session",
		CWD:         "/repo/root",
		FirstPrompt: "Initial prompt for feature",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	fake.Sessions = []model.SessionMeta{rootMeta}
	fake.Transcripts["s1"] = &model.Transcript{
		Meta: rootMeta,
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "Initial prompt for feature"}}},
			{Role: model.RoleAssistant, Parts: []model.Part{{Kind: model.PartText, Text: "Understood, working on it."}}},
		},
	}

	// 1. Default output: prints prompt markdown
	code, out, diag := runFake([]string{"handoff", "claude-code", "s1", "--target", "codex"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("handoff failed: exit=%d, diag=%s", code, diag)
	}
	if !strings.Contains(out, "Initial prompt for feature") && !strings.Contains(out, "Understood, working on it.") {
		t.Errorf("prompt output missing session content: %s", out)
	}

	// 2. --command output
	code, out, diag = runFake([]string{"handoff", "claude-code", "s1", "--target", "codex", "--command"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("handoff --command failed: exit=%d, diag=%s", code, diag)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "cd /repo/root && codex") {
		t.Errorf("unexpected command output: %s", out)
	}

	// 3. --cwd override
	code, out, diag = runFake([]string{"handoff", "claude-code", "s1", "--target", "opencode", "--cwd", "/custom/workdir", "--command"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("handoff with --cwd failed: exit=%d, diag=%s", code, diag)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "cd /custom/workdir && opencode --prompt") {
		t.Errorf("unexpected cwd command output: %s", out)
	}

	// 4. --json output
	code, out, diag = runFake([]string{"handoff", "claude-code", "s1", "--target", "codex", "--budget", "compact", "--json"}, provider.Set{fake})
	if code != 0 {
		t.Fatalf("handoff --json failed: exit=%d, diag=%s", code, diag)
	}
	var res struct {
		Prompt      string `json:"prompt"`
		Command     string `json:"command"`
		PromptFile  string `json:"promptFile"`
		PromptBytes int    `json:"promptBytes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad json: %v (%q)", err, out)
	}
	if res.Prompt == "" || res.Command == "" || res.PromptBytes == 0 {
		t.Errorf("unexpected empty fields in json: %+v", res)
	}
	if !strings.HasPrefix(res.PromptFile, dataHome) || !strings.Contains(res.Command, "Read "+res.PromptFile) {
		t.Errorf("command should point at a prompt file in the data dir: %+v", res)
	}
	if saved, err := os.ReadFile(res.PromptFile); err != nil || string(saved) != res.Prompt {
		t.Errorf("prompt file does not hold the prompt: %v", err)
	}
}
