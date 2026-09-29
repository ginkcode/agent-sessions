package handoff

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// FileOp tracks operations on files.
type FileOp string

const (
	OpCreated FileOp = "created"
	OpEdited  FileOp = "edited"
	OpDeleted FileOp = "deleted"
)

// WorkingState captures the extracted context from tool executions.
type WorkingState struct {
	Files       map[string]FileOp `json:"files"`
	Commands    []CommandRun      `json:"commands"`
	LatestTodo  string            `json:"latestTodo,omitempty"`
	Plans       []string          `json:"plans,omitempty"`
	Subagents   []SubagentSummary `json:"subagents,omitempty"`
	Errors      []string          `json:"errors,omitempty"`
	Compaction  string            `json:"compaction,omitempty"`
}

// CommandRun records a shell command invocation and its status.
type CommandRun struct {
	Command string `json:"command"`
	Status  string `json:"status"` // "ok" or "failed"
	Error   string `json:"error,omitempty"`
}

// SubagentSummary describes a delegated subagent task.
type SubagentSummary struct {
	AgentID     string `json:"agentId,omitempty"`
	Description string `json:"description"`
	Result      string `json:"result,omitempty"`
}

// ExtractWorkingState scans transcripts for tool calls, plans, and files.
func ExtractWorkingState(transcripts []model.Transcript) WorkingState {
	state := WorkingState{
		Files: make(map[string]FileOp),
	}

	for tIdx := range transcripts {
		tr := &transcripts[tIdx]
		for mIdx := range tr.Messages {
			msg := &tr.Messages[mIdx]
			for pIdx := range msg.Parts {
				part := &msg.Parts[pIdx]
				if part.Kind == model.PartCompaction && part.Text != "" {
					state.Compaction = part.Text
				}
				if part.Kind == model.PartPatch {
					for _, f := range part.Files {
						if _, exists := state.Files[f]; !exists {
							state.Files[f] = OpEdited
						}
					}
				}
				if part.Tool != nil {
					extractToolState(part.Tool, &state)
				}
			}
		}
	}

	return state
}

func extractToolState(tool *model.ToolCall, state *WorkingState) {
	nameLower := strings.ToLower(tool.Name)

	// 1. Files
	switch nameLower {
	case "edit", "write", "multiedit", "notebookedit":
		var input struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
			Path         string `json:"path"`
		}
		if len(tool.Input) > 0 {
			_ = json.Unmarshal(tool.Input, &input)
		}
		p := input.FilePath
		if p == "" {
			p = input.NotebookPath
		}
		if p == "" {
			p = input.Path
		}
		if p != "" {
			cleanP := filepath.Clean(p)
			if nameLower == "write" {
				if _, exists := state.Files[cleanP]; !exists {
					state.Files[cleanP] = OpCreated
				}
			} else {
				if _, exists := state.Files[cleanP]; !exists {
					state.Files[cleanP] = OpEdited
				}
			}
		}
	case "apply_patch", "patch":
		extractPatchFiles(string(tool.Input), state)
	}

	// 2. Shell commands
	if nameLower == "bash" || nameLower == "local_shell_call" || nameLower == "exec" || nameLower == "shell" {
		var input struct {
			Command string `json:"command"`
			Cmd     string `json:"cmd"`
		}
		if len(tool.Input) > 0 {
			_ = json.Unmarshal(tool.Input, &input)
		}
		cmd := input.Command
		if cmd == "" {
			cmd = input.Cmd
		}
		if cmd != "" {
			status := "ok"
			errMsg := ""
			if tool.Status == model.ToolError || strings.Contains(strings.ToLower(tool.Output), "error:") || strings.Contains(strings.ToLower(tool.Output), "failed") {
				if tool.Status == model.ToolError {
					status = "failed"
					errMsg = model.TruncateUTF8(tool.Output, 200)
				}
			}
			state.Commands = append(state.Commands, CommandRun{
				Command: cmd,
				Status:  status,
				Error:   errMsg,
			})
		}
	}

	// 3. Todos / Plans
	if strings.Contains(nameLower, "todo") || nameLower == "update_plan" {
		var input map[string]any
		if len(tool.Input) > 0 && json.Unmarshal(tool.Input, &input) == nil {
			if todos, ok := input["todos"]; ok {
				if todoBytes, err := json.MarshalIndent(todos, "", "  "); err == nil {
					state.LatestTodo = string(todoBytes)
				}
			} else {
				state.LatestTodo = string(tool.Input)
			}
		}
	}

	if nameLower == "exitplanmode" || nameLower == "plan" {
		var input struct {
			Plan string `json:"plan"`
		}
		if len(tool.Input) > 0 && json.Unmarshal(tool.Input, &input) == nil && input.Plan != "" {
			state.Plans = append(state.Plans, input.Plan)
		} else if len(tool.Input) > 0 {
			state.Plans = append(state.Plans, string(tool.Input))
		}
	}

	// 4. Subagents
	if nameLower == "agent" || nameLower == "task" {
		var input struct {
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		}
		if len(tool.Input) > 0 {
			_ = json.Unmarshal(tool.Input, &input)
		}
		desc := input.Description
		if desc == "" {
			desc = input.Prompt
		}
		if desc != "" {
			res := model.TruncateUTF8(tool.Output, 300)
			state.Subagents = append(state.Subagents, SubagentSummary{
				Description: desc,
				Result:      res,
			})
		}
	}

	// 5. Tool failures and errors
	if tool.Status == model.ToolError {
		snippet := model.TruncateUTF8(tool.Output, 500)
		if snippet == "" {
			snippet = fmt.Sprintf("Tool %s failed", tool.Name)
		}
		state.Errors = append(state.Errors, fmt.Sprintf("%s: %s", tool.Name, snippet))
	}
}

func extractPatchFiles(input string, state *WorkingState) {
	lines := strings.Split(input, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "*** ") || strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
			parts := strings.Fields(l)
			if len(parts) >= 2 && parts[1] != "/dev/null" {
				p := filepath.Clean(strings.TrimPrefix(parts[1], "a/"))
				p = strings.TrimPrefix(p, "b/")
				if _, ok := state.Files[p]; !ok {
					state.Files[p] = OpEdited
				}
			}
		}
	}
}
