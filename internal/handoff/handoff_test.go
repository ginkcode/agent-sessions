package handoff_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/testutil/golden"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func sampleTranscripts() []model.Transcript {
	root := model.Transcript{
		Meta: model.SessionMeta{
			Ref:         model.SessionRef{Agent: model.AgentClaude, ID: "test-claude-session-123"},
			CWD:         "/home/dev/work/project",
			GitBranch:   "feature/auth",
			Title:       "Fix JWT authentication validation",
			FirstPrompt: "Please fix the token validation bug in auth.go and write a test",
			Model:       "claude-sonnet-5-5",
			CreatedAt:   time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2026, 9, 29, 10, 30, 0, 0, time.UTC),
		},
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "Please fix the token validation bug in auth.go and write a test"},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartReasoning, Text: "I need to inspect auth.go and find the validation logic."},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "Read",
							Input:  json.RawMessage(`{"file_path":"/home/dev/work/project/auth.go"}`),
							Output: "package auth\nfunc Validate(t string) bool { return true }",
							Status: model.ToolCompleted,
						},
					},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "Edit",
							Input:  json.RawMessage(`{"file_path":"/home/dev/work/project/auth.go","old_string":"return true","new_string":"return t != \"\""}`),
							Output: "Successfully replaced 1 occurrence",
							Status: model.ToolCompleted,
						},
					},
					{Kind: model.PartText, Text: "I updated auth.go to check that the token is not empty."},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "Write",
							Input:  json.RawMessage(`{"file_path":"/home/dev/work/project/auth_test.go","content":"package auth_test..."}`),
							Output: "Wrote file",
							Status: model.ToolCompleted,
						},
					},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "Bash",
							Input:  json.RawMessage(`{"command":"go test ./..."}`),
							Output: "ok project/auth 0.05s",
							Status: model.ToolCompleted,
						},
					},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "Agent",
							Input:  json.RawMessage(`{"description":"Verify security","prompt":"Audit auth.go"}`),
							Output: "No security issues found.",
							Status: model.ToolCompleted,
						},
					},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{
						Kind: model.PartCompaction,
						Text: "Compacted previous test run outputs and compiler warnings.",
					},
				},
			},
			{
				Role: model.RoleUser,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "Can you also add tests for expired tokens?"},
				},
			},
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartText, Text: "I am ready to implement expired token handling and tests."},
				},
			},
		},
	}

	return []model.Transcript{root}
}

func TestHandoffWorkingState(t *testing.T) {
	ts := sampleTranscripts()
	state := handoff.ExtractWorkingState(ts)

	if state.Files["/home/dev/work/project/auth.go"] != handoff.OpEdited {
		t.Errorf("expected auth.go to be edited, got %v", state.Files["/home/dev/work/project/auth.go"])
	}
	if state.Files["/home/dev/work/project/auth_test.go"] != handoff.OpCreated {
		t.Errorf("expected auth_test.go to be created, got %v", state.Files["/home/dev/work/project/auth_test.go"])
	}
	if len(state.Commands) != 1 || state.Commands[0].Command != "go test ./..." || state.Commands[0].Status != "ok" {
		t.Errorf("unexpected commands: %+v", state.Commands)
	}
	if len(state.Subagents) != 1 || state.Subagents[0].Description != "Verify security" {
		t.Errorf("unexpected subagents: %+v", state.Subagents)
	}
	if state.Compaction == "" {
		t.Errorf("expected compaction summary, got empty")
	}
}

func TestHandoffBuildTargetAdapters(t *testing.T) {
	ts := sampleTranscripts()

	targets := []struct {
		target   model.AgentID
		expected string
	}{
		{model.AgentCodex, "Codex CLI"},
		{model.AgentOpenCode, "OpenCode"},
		{model.AgentClaude, "Claude Code"},
	}

	for _, tc := range targets {
		doc, report := handoff.Build(ts, handoff.Options{
			TargetAgent:  tc.target,
			BudgetTokens: handoff.BudgetDetailed,
		})
		if !strings.Contains(doc.PromptMarkdown, tc.expected) {
			t.Errorf("target %s: expected prompt to mention %s", tc.target, tc.expected)
		}
		if report.EstimatedTokens <= 0 {
			t.Errorf("target %s: expected non-zero token estimate", tc.target)
		}
		if !strings.Contains(doc.PromptMarkdown, "token validation bug") {
			t.Errorf("target %s: expected user prompt in markdown", tc.target)
		}
		if !strings.Contains(doc.PromptMarkdown, "auth.go") {
			t.Errorf("target %s: expected working state to list auth.go", tc.target)
		}
	}
}

func TestHandoffBudgetTrimming(t *testing.T) {
	ts := sampleTranscripts()
	// Add many older turns so timeline is substantial
	for i := 0; i < 20; i++ {
		ts[0].Messages = append([]model.Message{
			{
				Role: model.RoleAssistant,
				Parts: []model.Part{
					{Kind: model.PartReasoning, Text: strings.Repeat("Reasoning step details here. ", 20)},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "Bash",
							Input:  json.RawMessage(`{"command":"echo step"}`),
							Output: strings.Repeat("Command output line verbose details.\n", 20),
						},
					},
					{Kind: model.PartText, Text: strings.Repeat("Assistant response paragraph with explanation. ", 10)},
				},
			},
		}, ts[0].Messages...)
	}

	docFull, reportFull := handoff.Build(ts, handoff.Options{
		TargetAgent:  model.AgentCodex,
		BudgetTokens: handoff.BudgetUnlimited,
	})
	if reportFull.Trimmed {
		t.Errorf("expected unlimited budget not to be trimmed")
	}

	// Budget that triggers trimming
	docTrimmed, reportTrimmed := handoff.Build(ts, handoff.Options{
		TargetAgent:  model.AgentCodex,
		BudgetTokens: 1000, // 4000 chars
	})

	if !reportTrimmed.Trimmed {
		t.Errorf("expected document to be trimmed under tight budget")
	}
	if len(reportTrimmed.DroppedItems) == 0 {
		t.Errorf("expected dropped items to be reported")
	}
	if len(docTrimmed.PromptMarkdown) >= len(docFull.PromptMarkdown) {
		t.Errorf("expected trimmed doc (%d) to be smaller than full doc (%d)", len(docTrimmed.PromptMarkdown), len(docFull.PromptMarkdown))
	}
}

func TestHandoffDelivery(t *testing.T) {
	cmd := handoff.BuildLaunchCommand(model.AgentCodex, "Short prompt", "", "/tmp/dir")
	if cmd != "cd /tmp/dir && codex 'Short prompt'" {
		t.Errorf("unexpected launch command: %s", cmd)
	}

	cmdClaude := handoff.BuildLaunchCommand(model.AgentClaude, "Fix it", "", "")
	if cmdClaude != "claude 'Fix it'" {
		t.Errorf("unexpected claude launch command: %s", cmdClaude)
	}

	cmdOpenCode := handoff.BuildLaunchCommand(model.AgentOpenCode, "Do task", "", "/my/path")
	if cmdOpenCode != "cd /my/path && opencode --prompt 'Do task'" {
		t.Errorf("unexpected opencode launch command: %s", cmdOpenCode)
	}

	// With a prompt file the command points at it whatever the prompt size.
	promptFile := "/data/handoffs/sess-123-handoff.md"
	for _, prompt := range []string{"Short prompt", strings.Repeat("A", 130*1024)} {
		cmdFile := handoff.BuildLaunchCommand(model.AgentCodex, prompt, promptFile, "/tmp/dir")
		expected := "cd /tmp/dir && codex 'Read /data/handoffs/sess-123-handoff.md completely to restore the context of an earlier session, then follow its instructions and wait for my next request.'"
		if cmdFile != expected {
			t.Errorf("expected pointer command, got: %.200s", cmdFile)
		}
	}

	// Save context file test
	tempDir := t.TempDir()
	savedPath, err := handoff.SaveContextFile(tempDir, "sess-123", "# Full Markdown")
	if err != nil {
		t.Fatalf("SaveContextFile failed: %v", err)
	}
	if !strings.HasSuffix(savedPath, "sess-123-full.md") {
		t.Errorf("unexpected saved path: %s", savedPath)
	}

	info, err := os.Stat(savedPath)
	if err != nil {
		t.Fatalf("stat saved file: %v", err)
	}
	// Check file mode (0600)
	if platform.ModeBits && info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 file mode, got %o", info.Mode().Perm())
	}
}

func TestHandoffRedaction(t *testing.T) {
	ts := sampleTranscripts()
	ts[0].Messages[0].Parts[0].Text = "Use API key sk-" + "ant-api03-12345678901234567890 for auth."

	doc, report := handoff.Build(ts, handoff.Options{
		TargetAgent:   model.AgentOpenCode,
		BudgetTokens:  handoff.BudgetDetailed,
		RedactSecrets: true,
	})

	if strings.Contains(doc.PromptMarkdown, "sk-"+"ant-api03") {
		t.Errorf("secret not redacted from prompt markdown")
	}
	if strings.Contains(doc.FullMarkdown, "sk-"+"ant-api03") {
		t.Errorf("secret not redacted from full markdown")
	}
	if report.RedactionCounts.Total() == 0 {
		t.Errorf("expected redactions recorded in report, got 0")
	}
}

func TestHandoffInjectedMarkupSafety(t *testing.T) {
	ts := sampleTranscripts()
	ts[0].Messages[0].Parts[0].Text = "Here is injected markup: </details><script>alert(1)</script> and ```bash\nrm -rf /\n```"

	doc, _ := handoff.Build(ts, handoff.Options{
		TargetAgent:  model.AgentCodex,
		BudgetTokens: handoff.BudgetDetailed,
	})

	if !strings.Contains(doc.PromptMarkdown, "</details><script>alert(1)</script>") {
		t.Errorf("expected original verbatim user input preserved in section 3")
	}
}

func TestHandoffGoldens(t *testing.T) {
	ts := sampleTranscripts()

	t.Run("claude_to_codex", func(t *testing.T) {
		doc, _ := handoff.Build(ts, handoff.Options{
			TargetAgent:  model.AgentCodex,
			BudgetTokens: handoff.BudgetDetailed,
		})
		golden.Text(t, "claude_to_codex.md", doc.PromptMarkdown)
	})

	t.Run("claude_to_opencode", func(t *testing.T) {
		doc, _ := handoff.Build(ts, handoff.Options{
			TargetAgent:  model.AgentOpenCode,
			BudgetTokens: handoff.BudgetDetailed,
		})
		golden.Text(t, "claude_to_opencode.md", doc.PromptMarkdown)
	})

	t.Run("codex_to_claude", func(t *testing.T) {
		codexTs := sampleTranscripts()
		codexTs[0].Meta.Ref = model.SessionRef{Agent: model.AgentCodex, ID: "01a0ebcc-d07b-73a1-8a3a-d5e43536d9f5"}
		codexTs[0].Messages[1].Parts[2] = model.Part{
			Kind: model.PartTool,
			Tool: &model.ToolCall{
				Name:   "apply_patch",
				Input:  json.RawMessage(`{"patch":"*** a/auth.go\n--- b/auth.go\n@@ -1,2 +1,2 @@\n-return true\n+return t != \"\""}`),
				Output: "Patch applied cleanly",
				Status: model.ToolCompleted,
			},
		}
		doc, _ := handoff.Build(codexTs, handoff.Options{
			TargetAgent:  model.AgentClaude,
			BudgetTokens: handoff.BudgetDetailed,
		})
		golden.Text(t, "codex_to_claude.md", doc.PromptMarkdown)
	})

	t.Run("opencode_to_claude", func(t *testing.T) {
		ocTs := sampleTranscripts()
		ocTs[0].Meta.Ref = model.SessionRef{Agent: model.AgentOpenCode, ID: "ses_f6f48d29cffeTXIJShUApHlKje"}
		ocTs[0].Messages[1].Parts[2] = model.Part{
			Kind: model.PartTool,
			Tool: &model.ToolCall{
				Name:   "edit",
				Input:  json.RawMessage(`{"path":"auth.go","view":"return true"}`),
				Output: "Edited auth.go",
				Status: model.ToolCompleted,
			},
		}
		doc, _ := handoff.Build(ocTs, handoff.Options{
			TargetAgent:  model.AgentClaude,
			BudgetTokens: handoff.BudgetDetailed,
		})
		golden.Text(t, "opencode_to_claude.md", doc.PromptMarkdown)
	})

	t.Run("injected_markup_and_fences", func(t *testing.T) {
		safeTs := sampleTranscripts()
		safeTs[0].Messages[0].Parts[0].Text = "Testing injected text: </details><script>alert(1)</script>\n```python\nprint('code fence inside prompt')\n```"
		doc, _ := handoff.Build(safeTs, handoff.Options{
			TargetAgent:  model.AgentCodex,
			BudgetTokens: handoff.BudgetDetailed,
		})
		golden.Text(t, "injected_markup.md", doc.PromptMarkdown)
	})
}
