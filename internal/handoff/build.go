package handoff

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/redact"
)

// TargetAdapter provides target-specific instructions and tool vocabulary.
type TargetAdapter struct {
	TargetID    model.AgentID
	Name        string
	ToolMapping map[string]string
}

func getAdapter(target model.AgentID, source model.AgentID) TargetAdapter {
	adapter := TargetAdapter{
		TargetID:    target,
		Name:        string(target),
		ToolMapping: make(map[string]string),
	}
	switch target {
	case model.AgentClaude:
		adapter.Name = "Claude Code"
		adapter.ToolMapping = map[string]string{
			"apply_patch / edit / write": "Edit or Write",
			"local_shell_call / bash":    "Bash",
			"read / read_file":           "Read",
		}
	case model.AgentCodex:
		adapter.Name = "Codex CLI"
		adapter.ToolMapping = map[string]string{
			"Edit / Write / MultiEdit": "apply_patch",
			"Bash":                     "shell / local_shell_call",
			"Read":                     "read_file",
		}
	case model.AgentOpenCode:
		adapter.Name = "OpenCode"
		adapter.ToolMapping = map[string]string{
			"Edit / Write / apply_patch": "edit / write / patch",
			"Bash / local_shell_call":    "bash",
			"Read":                       "read",
		}
	default:
		adapter.Name = "coding assistant"
	}
	return adapter
}

func sourceName(agent model.AgentID) string {
	switch agent {
	case model.AgentClaude:
		return "Claude Code"
	case model.AgentCodex:
		return "Codex CLI"
	case model.AgentOpenCode:
		return "OpenCode"
	default:
		return string(agent)
	}
}

// Build creates the handoff document and report from session transcripts.
func Build(transcripts []model.Transcript, opts Options) (Doc, Report) {
	if len(transcripts) == 0 {
		return Doc{}, Report{}
	}

	root := transcripts[0]
	adapter := getAdapter(opts.TargetAgent, root.Meta.Ref.Agent)
	state := ExtractWorkingState(transcripts)

	// Collect user messages and turns
	var userMsgs []string
	var allTurns []model.Message
	for _, tr := range transcripts {
		for _, m := range tr.Messages {
			if m.IsMeta {
				continue
			}
			allTurns = append(allTurns, m)
			if m.Role == model.RoleUser {
				text := extractTurnText(&m)
				if text != "" {
					userMsgs = append(userMsgs, text)
				}
			}
		}
	}

	// Partition turns into earlier timeline vs last N turns (last 3 turns)
	lastCount := 3
	splitIdx := len(allTurns) - lastCount
	if splitIdx < 0 {
		splitIdx = 0
	}
	timelineTurns := allTurns[:splitIdx]
	lastTurns := allTurns[splitIdx:]

	// 1. Preamble
	s1 := buildPreamble(root.Meta, adapter, opts)

	// 2. Facts
	s2 := buildFacts(root.Meta, opts)

	// 3. Goal and user messages
	s3 := buildUserMessages(root.Meta, userMsgs)

	// 4. Latest compaction summary
	s4 := buildCompaction(state)

	// 5. Working state
	s5 := buildWorkingState(state)

	// 6. Timeline
	s6 := buildTimeline(timelineTurns, opts.IncludeReasoning)

	// 7. Last N turns verbatim
	s7 := buildLastTurns(lastTurns, opts.IncludeReasoning)

	// 8. Closing
	s8 := buildClosing(allTurns, opts)

	// Build FullMarkdown (untrimmed sections 1-8)
	fullParts := []string{s1, s2, s3}
	if s4 != "" {
		fullParts = append(fullParts, s4)
	}
	fullParts = append(fullParts, s5, s6, s7, s8)
	fullDoc := strings.Join(fullParts, "\n\n")

	// Build PromptMarkdown: core sections (1-5, 7, 8) + timeline (trimmed to budget)
	var report Report
	report.BudgetTokens = opts.BudgetTokens

	promptDoc, trimmed, dropped := trimToBudget(s1, s2, s3, s4, s5, s6, s7, s8, timelineTurns, opts)
	report.Trimmed = trimmed
	report.DroppedItems = dropped

	// Redaction if requested
	if opts.RedactSecrets {
		var c1, c2 redact.Counts
		promptDoc, c1 = redact.Text(promptDoc)
		fullDoc, c2 = redact.Text(fullDoc)
		report.RedactionCounts = c1.Add(c2)
	}

	report.EstimatedTokens = len(promptDoc) / 4

	return Doc{
		PromptMarkdown: promptDoc,
		FullMarkdown:   fullDoc,
		Report:         report,
	}, report
}

func buildPreamble(meta model.SessionMeta, adapter TargetAdapter, opts Options) string {
	var b strings.Builder
	src := sourceName(meta.Ref.Agent)
	b.WriteString(fmt.Sprintf("# Continuation Context\n\n> Target Agent: **%s**\n> You are continuing an engineering session started in **%s**.\n>\n", adapter.Name, src))
	b.WriteString("> **Instructions:**\n")
	b.WriteString("> 1. This document restores context only. The last request below may already be complete.\n")
	b.WriteString("> 2. Do NOT redo completed work listed under Working State.\n")
	b.WriteString("> 3. Inspect the repository first (`git status` and `git diff`) to verify local working tree state.\n")
	b.WriteString("> 4. Do not change files or run state-changing commands yet. Briefly summarize where the work stands, then wait for the user to confirm or give the next request.\n")

	if len(adapter.ToolMapping) > 0 {
		b.WriteString(fmt.Sprintf(">\n> **Tool mapping for %s:**\n", adapter.Name))
		var keys []string
		for k := range adapter.ToolMapping {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("> - `%s` ≈ `%s`\n", k, adapter.ToolMapping[k]))
		}
	}

	if opts.ContextFilePath != "" {
		b.WriteString(fmt.Sprintf(">\n> *Full conversation history is preserved in `%s`. Read it on demand if you need complete earlier outputs.*\n", opts.ContextFilePath))
	}

	return b.String()
}

func buildFacts(meta model.SessionMeta, opts Options) string {
	var b strings.Builder
	b.WriteString("## Session Facts\n\n")
	b.WriteString(fmt.Sprintf("- **Source Agent:** %s\n", sourceName(meta.Ref.Agent)))
	b.WriteString(fmt.Sprintf("- **Session ID:** `%s`\n", meta.Ref.ID))
	b.WriteString(fmt.Sprintf("- **Original Directory:** `%s`\n", meta.CWD))
	if opts.RemappedCWD != "" && opts.RemappedCWD != meta.CWD {
		b.WriteString(fmt.Sprintf("- **Current Directory:** `%s`\n", opts.RemappedCWD))
	}
	if meta.GitBranch != "" {
		b.WriteString(fmt.Sprintf("- **Branch:** `%s`\n", meta.GitBranch))
	}
	if meta.Model != "" {
		b.WriteString(fmt.Sprintf("- **Model:** %s\n", meta.Model))
	}
	if !meta.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- **Started:** %s\n", meta.CreatedAt.Format(time.RFC3339)))
	}
	if !meta.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- **Last Active:** %s\n", meta.UpdatedAt.Format(time.RFC3339)))
	}
	resumeCmd := formatOriginalResume(meta.Ref.Agent, meta.Ref.ID)
	if resumeCmd != "" {
		b.WriteString(fmt.Sprintf("- **Original Resume:** `%s`\n", resumeCmd))
	}
	return b.String()
}

func formatOriginalResume(agent model.AgentID, id string) string {
	switch agent {
	case model.AgentClaude:
		return fmt.Sprintf("claude --resume %s", id)
	case model.AgentCodex:
		return fmt.Sprintf("codex resume %s", id)
	case model.AgentOpenCode:
		return fmt.Sprintf("opencode --session %s", id)
	default:
		return ""
	}
}

func buildUserMessages(meta model.SessionMeta, userMsgs []string) string {
	var b strings.Builder
	b.WriteString("## User Intent & Prompts\n\n")
	goal := meta.FirstPrompt
	if goal == "" && len(userMsgs) > 0 {
		goal = userMsgs[0]
	}
	if goal != "" {
		b.WriteString(fmt.Sprintf("**Primary Goal:**\n> %s\n\n", strings.ReplaceAll(strings.TrimSpace(goal), "\n", "\n> ")))
	}
	b.WriteString("**All User Messages:**\n")
	for i, msg := range userMsgs {
		trimmed := strings.TrimSpace(msg)
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, trimmed))
	}
	return b.String()
}

func buildCompaction(state WorkingState) string {
	if state.Compaction == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Prior Compaction Summary\n\n")
	b.WriteString(strings.TrimSpace(state.Compaction))
	b.WriteString("\n")
	return b.String()
}

func buildWorkingState(state WorkingState) string {
	var b strings.Builder
	b.WriteString("## Working State\n\n")

	// Files
	if len(state.Files) > 0 {
		b.WriteString("### Modified Files\n")
		var paths []string
		for p := range state.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			b.WriteString(fmt.Sprintf("- `%s` (%s)\n", p, state.Files[p]))
		}
		b.WriteString("\n")
	}

	// Commands
	if len(state.Commands) > 0 {
		b.WriteString("### Executed Commands\n")
		// Keep last 15 commands to stay crisp
		cmds := state.Commands
		if len(cmds) > 15 {
			cmds = cmds[len(cmds)-15:]
		}
		for _, c := range cmds {
			statusMark := "✓"
			if c.Status == "failed" {
				statusMark = "✗ (" + c.Error + ")"
			}
			b.WriteString(fmt.Sprintf("- `%s` %s\n", c.Command, statusMark))
		}
		b.WriteString("\n")
	}

	// Latest Todo
	if state.LatestTodo != "" {
		b.WriteString("### Active Task List\n```json\n")
		b.WriteString(strings.TrimSpace(state.LatestTodo))
		b.WriteString("\n```\n\n")
	}

	// Plans
	if len(state.Plans) > 0 {
		b.WriteString("### Approved Plan\n")
		lastPlan := state.Plans[len(state.Plans)-1]
		b.WriteString(strings.TrimSpace(lastPlan))
		b.WriteString("\n\n")
	}

	// Subagents
	if len(state.Subagents) > 0 {
		b.WriteString("### Subagent Tasks\n")
		for _, s := range state.Subagents {
			b.WriteString(fmt.Sprintf("- **Task:** %s\n", s.Description))
			if s.Result != "" {
				b.WriteString(fmt.Sprintf("  - **Result:** %s\n", s.Result))
			}
		}
		b.WriteString("\n")
	}

	// Errors
	if len(state.Errors) > 0 {
		b.WriteString("### Recent Tool Errors & Failures\n")
		for _, e := range state.Errors {
			b.WriteString(fmt.Sprintf("- %s\n", e))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func buildTimeline(turns []model.Message, includeReasoning bool) string {
	if len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Recent Conversation Timeline\n\n")
	for _, m := range turns {
		renderTurn(&b, m, includeReasoning, false)
	}
	return b.String()
}

func buildLastTurns(turns []model.Message, includeReasoning bool) string {
	if len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Last Conversation Turns\n\n")
	for _, m := range turns {
		renderTurn(&b, m, includeReasoning, true)
	}
	return b.String()
}

func renderTurn(b *strings.Builder, m model.Message, includeReasoning, fullOutput bool) {
	var body strings.Builder
	for _, p := range m.Parts {
		switch p.Kind {
		case model.PartText:
			if strings.TrimSpace(p.Text) != "" {
				body.WriteString(strings.TrimSpace(p.Text))
				body.WriteString("\n\n")
			}
		case model.PartReasoning:
			if includeReasoning && strings.TrimSpace(p.Text) != "" {
				body.WriteString("> *Reasoning:*\n> ")
				body.WriteString(strings.ReplaceAll(strings.TrimSpace(p.Text), "\n", "\n> "))
				body.WriteString("\n\n")
			}
		case model.PartTool:
			if p.Tool != nil {
				renderTool(&body, p.Tool, fullOutput)
			}
		}
	}
	if body.Len() == 0 {
		return
	}
	roleLabel := "User"
	if m.Role == model.RoleAssistant {
		roleLabel = "Assistant"
	}
	b.WriteString(fmt.Sprintf("### %s\n", roleLabel))
	b.WriteString(body.String())
}

func renderTool(b *strings.Builder, tool *model.ToolCall, fullOutput bool) {
	b.WriteString(fmt.Sprintf("- **Tool: `%s`**\n", tool.Name))
	if len(tool.Input) > 0 {
		summary := model.JSONPlainValues(tool.Input, 200)
		if summary != "" {
			b.WriteString(fmt.Sprintf("  - *Input:* `%s`\n", summary))
		}
	}
	if tool.Output != "" {
		out := strings.TrimSpace(tool.Output)
		if fullOutput || len(out) <= 300 {
			b.WriteString("  - *Output:*\n  ```\n  ")
			b.WriteString(strings.ReplaceAll(out, "\n", "\n  "))
			b.WriteString("\n  ```\n")
		} else {
			// Head / tail
			head := model.TruncateUTF8(out, 150)
			tail := ""
			if len(out) > 150 {
				tail = out[len(out)-150:]
			}
			b.WriteString(fmt.Sprintf("  - *Output snippet:* `%s ... %s`\n", model.OneLine(head), model.OneLine(tail)))
		}
	}
	b.WriteString("\n")
}

func buildClosing(turns []model.Message, opts Options) string {
	var b strings.Builder
	b.WriteString("## Next Steps & Continuation\n\n")

	var lastUser, lastAssistant string
	for i := len(turns) - 1; i >= 0; i-- {
		m := turns[i]
		if lastUser == "" && m.Role == model.RoleUser {
			lastUser = extractTurnText(&m)
		}
		if lastAssistant == "" && m.Role == model.RoleAssistant {
			lastAssistant = extractTurnText(&m)
		}
		if lastUser != "" && lastAssistant != "" {
			break
		}
	}

	if lastUser != "" {
		b.WriteString(fmt.Sprintf("**Last User Request:**\n> %s\n\n", strings.ReplaceAll(strings.TrimSpace(lastUser), "\n", "\n> ")))
	}
	if lastAssistant != "" {
		b.WriteString(fmt.Sprintf("**Last Assistant State:**\n> %s\n\n", strings.ReplaceAll(strings.TrimSpace(lastAssistant), "\n", "\n> ")))
	}

	b.WriteString("**Before continuing:** The request above may already be done; do not resume it on your own. Reply with a short summary of the current state and anything left unfinished, then wait for the user to confirm or give the next request.\n")
	return b.String()
}

func extractTurnText(m *model.Message) string {
	var texts []string
	for _, p := range m.Parts {
		if p.Kind == model.PartText && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, strings.TrimSpace(p.Text))
		}
	}
	return strings.Join(texts, "\n\n")
}
