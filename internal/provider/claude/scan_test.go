package claude

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func fixtureRoot(t *testing.T, name string) (string, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "project-x")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, name+".jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func scanOne(t *testing.T, name string) model.SessionMeta {
	t.Helper()
	root, _ := fixtureRoot(t, name)
	result, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(result.Changed))
	}
	return result.Changed[0]
}

func TestScanFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prompt  string
		title   string
		counts  model.MessageCounts
		tokens  model.TokenUsage
		model   string
		created string
		updated string
	}{
		{
			name: "basic", prompt: "Fix the login bug", title: "Fix login token validation",
			counts: model.MessageCounts{User: 2, Assistant: 2, ToolCalls: 1},
			tokens: model.TokenUsage{Input: 250, Output: 75, CacheRead: 30, CacheWrite: 10},
			model:  "claude-sonnet-5", created: "2026-09-20T10:00:00Z", updated: "2026-09-20T10:01:31Z",
		},
		{
			name: "split_assistant", prompt: "Explain this function", title: "Explain this function",
			counts: model.MessageCounts{User: 1, Assistant: 1, ToolCalls: 2},
			tokens: model.TokenUsage{Input: 50, Output: 30},
			model:  "claude-sonnet-5", created: "2026-09-20T11:00:00Z", updated: "2026-09-20T11:00:06Z",
		},
		{
			name: "compaction", prompt: "Summarize the work", title: "Continue with tests",
			counts: model.MessageCounts{User: 2, Assistant: 2},
			tokens: model.TokenUsage{Input: 500, Output: 70},
			model:  "claude-sonnet-5", created: "2026-09-20T12:00:00Z", updated: "2026-09-20T12:02:10Z",
		},
		{
			name: "meta", prompt: "Review the configuration", title: "Review the configuration",
			counts: model.MessageCounts{User: 1, Assistant: 1},
			tokens: model.TokenUsage{Input: 80, Output: 12},
			model:  "claude-sonnet-5", created: "2026-09-20T13:00:00Z", updated: "2026-09-20T13:01:10Z",
		},
		{
			name: "partial_last_line", prompt: "A complete prompt", title: "A complete prompt",
			counts: model.MessageCounts{User: 1, Assistant: 1},
			tokens: model.TokenUsage{Input: 10, Output: 5},
			model:  "claude-sonnet-5", created: "2026-09-20T14:00:00Z", updated: "2026-09-20T14:00:01Z",
		},
		{
			name: "unknown_types", prompt: "Show unknown records", title: "Show unknown records",
			counts: model.MessageCounts{User: 1, Assistant: 1},
			tokens: model.TokenUsage{Input: 20, Output: 8},
			model:  "claude-sonnet-5", created: "2026-09-20T15:00:00Z", updated: "2026-09-20T15:00:05Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := scanOne(t, tt.name)
			if meta.Ref != (model.SessionRef{Agent: model.AgentClaude, ID: tt.name}) {
				t.Errorf("Ref = %+v", meta.Ref)
			}
			if meta.CWD != "/home/dev/project" || !meta.CWDMissing || meta.RepoRoot != "" {
				t.Errorf("cwd/repo = %q, %t, %q", meta.CWD, meta.CWDMissing, meta.RepoRoot)
			}
			if meta.FirstPrompt != tt.prompt || meta.Title != tt.title || meta.Counts != tt.counts || meta.Tokens != tt.tokens || meta.Model != tt.model {
				t.Errorf("meta = %+v, want prompt=%q title=%q counts=%+v tokens=%+v model=%q", meta, tt.prompt, tt.title, tt.counts, tt.tokens, tt.model)
			}
			if meta.CreatedAt.Format(time.RFC3339) != tt.created || meta.UpdatedAt.Format(time.RFC3339) != tt.updated {
				t.Errorf("times = %s / %s, want %s / %s", meta.CreatedAt, meta.UpdatedAt, tt.created, tt.updated)
			}
			if tt.name == "basic" && (meta.GitBranch != "main" || meta.AgentVersion != "2.1.0") {
				t.Errorf("git/version = %q/%q", meta.GitBranch, meta.AgentVersion)
			}
		})
	}
}

func TestScanTitlePrecedenceAndUsage(t *testing.T) {
	t.Parallel()
	var cp checkpoint
	var d provider.Diagnostics
	lines := []string{
		`{"type":"user","timestamp":"2026-09-20T12:00:03+02:00","cwd":"/no/such/cwd/../dir","message":{"content":"<command-name>/help"}}`,
		`{"type":"user","timestamp":"2026-09-20T10:00:01Z","gitBranch":"main","version":"v1","message":{"content":[{"type":"tool_result","content":"output"},{"type":"text","text":"   Hello\n   world   "}]}}`,
		`{"type":"assistant","timestamp":"2026-09-20T10:00:02Z","gitBranch":"HEAD","message":{"id":"a","model":"<synthetic>","content":[{"type":"tool_use"}],"usage":{"input_tokens":5,"output_tokens":1,"output_tokens_details":{"thinking_tokens":1}}}}`,
		`{"type":"assistant","timestamp":"2026-09-20T10:00:04Z","gitBranch":"feature","version":"v2","message":{"id":"a","model":"claude-opus-5","content":[],"usage":{"input_tokens":5,"output_tokens":7,"output_tokens_details":{"thinking_tokens":3},"cache_read_input_tokens":2,"cache_creation_input_tokens":4}}}`,
		`{"type":"assistant","timestamp":"2026-09-20T10:00:05Z","message":{"id":"b","content":[],"usage":{"input_tokens":9,"output_tokens":2}}}`,
		`{"type":"summary","summary":"older title"}`,
		`{"type":"ai-title","aiTitle":"Newest AI title"}`,
	}
	for _, line := range lines {
		cp.consume([]byte(line), &d)
	}
	meta := cp.finalize(time.Time{})
	if meta.Counts != (model.MessageCounts{User: 1, Assistant: 2, ToolCalls: 1}) || meta.Tokens != (model.TokenUsage{Input: 14, Output: 9, Reasoning: 3, CacheRead: 2, CacheWrite: 4}) {
		t.Errorf("counts/tokens = %+v / %+v", meta.Counts, meta.Tokens)
	}
	if meta.FirstPrompt != "Hello world" || meta.Title != "Newest AI title" || meta.Model != "claude-opus-5" || meta.GitBranch != "feature" || meta.AgentVersion != "v2" || meta.CWD != "/no/such/dir" {
		t.Errorf("metadata = %+v", meta)
	}
	if meta.CreatedAt.Format(time.RFC3339) != "2026-09-20T10:00:01Z" || meta.UpdatedAt.Format(time.RFC3339) != "2026-09-20T10:00:05Z" {
		t.Errorf("times = %s / %s", meta.CreatedAt, meta.UpdatedAt)
	}
	if d.ParseErrors != 0 {
		t.Errorf("unexpected parse errors: %+v", d)
	}
	cp.AITitle = ""
	if got := cp.finalize(time.Time{}).Title; got != "older title" {
		t.Errorf("summary fallback = %q", got)
	}
	cp.Summary = ""
	if got := cp.finalize(time.Time{}).Title; got != "Hello world" {
		t.Errorf("prompt fallback = %q", got)
	}
	cp.Meta.FirstPrompt = ""
	cp.LastUserPrompt = ""
	if got := cp.finalize(time.Time{}).Title; got != "(untitled)" {
		t.Errorf("empty fallback = %q", got)
	}
}

func TestScanTitleSkipsAttachmentOnlyLatestPrompt(t *testing.T) {
	t.Parallel()
	var cp checkpoint
	var d provider.Diagnostics
	for _, line := range []string{
		`{"type":"user","message":{"content":"opening prompt"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"latest text prompt"}]}}`,
		`{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64","data":"AA=="}}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"user","message":{"content":"<task-notification> <task-id>a1</task-id> </task-notification>"}}`,
	} {
		cp.consume([]byte(line), &d)
	}
	meta := cp.finalize(time.Time{})
	if meta.Title != "latest text prompt" || meta.FirstPrompt != "opening prompt" || meta.Counts.User != 5 {
		t.Errorf("meta = %+v", meta)
	}
}

func TestScanSubagentTitleFromMetaTaskPrompt(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"type":"user","isMeta":true,"message":{"content":"Review target:\n  T-080 router changes"}}`,
		`{"type":"user","isMeta":true,"message":{"content":"later injected context"}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"output"}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	}
	consume := func(cp *checkpoint) model.SessionMeta {
		var d provider.Diagnostics
		for _, line := range lines {
			cp.consume([]byte(line), &d)
		}
		return cp.finalize(time.Time{})
	}

	sub := checkpoint{Meta: model.SessionMeta{ParentID: "parent"}}
	if got := consume(&sub).Title; got != "Review target: T-080 router changes" {
		t.Errorf("subagent title = %q", got)
	}
	sub.SubagentDescription = "Spawn description"
	if got := sub.finalize(time.Time{}).Title; got != "Spawn description" {
		t.Errorf("description precedence = %q", got)
	}
	// Main sessions keep ignoring meta records, which hold injected context.
	var main checkpoint
	if got := consume(&main).Title; got != "[Request interrupted by user]" {
		t.Errorf("main session title = %q", got)
	}
}

func TestScanSubagentAndMetadataChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "project-x")
	fixture := filepath.Join("testdata", "project-x")
	parent := "11111111-2222-3333-4444-555555555555"
	subdir := filepath.Join(project, parent, "subagents")
	if err := os.MkdirAll(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{parent + ".jsonl", filepath.Join(parent, "subagents", "agent-a1.jsonl"), filepath.Join(parent, "subagents", "agent-a1.meta.json")} {
		data, err := os.ReadFile(filepath.Join(fixture, rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, rel), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := New(root, nil)
	one, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Changed) != 2 {
		t.Fatalf("changed = %+v", one.Changed)
	}
	var child model.SessionMeta
	for _, m := range one.Changed {
		if m.ParentID != "" {
			child = m
		}
	}
	if child.Ref.ID != parent+"/agent-a1" || child.ParentID != parent || child.AgentName != "Explore" || child.Title != "Search for the target function" || child.ParentToolCallID != "parent-call-1" {
		t.Errorf("child metadata = %+v", child)
	}
	unchanged, err := p.Scan(context.Background(), one.State)
	if err != nil || len(unchanged.Changed) != 0 {
		t.Fatalf("unchanged = %+v, %v", unchanged.Changed, err)
	}
	metaPath := filepath.Join(subdir, "agent-a1.meta.json")
	if err := os.WriteFile(metaPath, []byte(`{"agentType":"Plan","description":"Find a newer function","toolUseId":"parent-call-2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	work := new(provider.ScanWork)
	changed, err := p.Scan(provider.WithScanWork(context.Background(), work), unchanged.State)
	if err != nil || len(changed.Changed) != 1 {
		t.Fatalf("changed = %+v, %v", changed.Changed, err)
	}
	// PF-09: a sidecar-only change resumes at the saved offset (one open, no
	// records), decoding and re-encoding only the subagent checkpoint.
	if work.SourceOpens.Load() != 1 || work.Records.Load() != 0 || work.CheckpointDecodes.Load() != 1 || work.CheckpointEncodes.Load() != 1 {
		t.Errorf("sidecar work opens/records/decodes/encodes = %d/%d/%d/%d", work.SourceOpens.Load(), work.Records.Load(), work.CheckpointDecodes.Load(), work.CheckpointEncodes.Load())
	}
	m := changed.Changed[0]
	if m.AgentName != "Plan" || m.Title != "Find a newer function" || m.ParentToolCallID != "parent-call-2" || m.Counts != child.Counts || m.Tokens != child.Tokens {
		t.Errorf("updated child = %+v", m)
	}
	if err := os.Remove(filepath.Join(subdir, "agent-a1.jsonl")); err != nil {
		t.Fatal(err)
	}
	removed, err := p.Scan(context.Background(), changed.State)
	if err != nil || len(removed.Removed) != 1 || removed.Removed[0] != child.Ref || len(removed.Changed) != 0 {
		t.Errorf("removed = %+v, %v", removed, err)
	}
}

func TestScanResumeAtEveryFixtureBoundary(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"basic", "split_assistant", "compaction", "meta", "partial_last_line", "unknown_types"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			content, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			// The partial fixture intentionally ends mid-line. The full-scan
			// comparison uses the same bytes, rather than completing its JSON.
			baseline := scanOne(t, name)
			// Cuts are strictly shorter than the file: rewriting the full
			// content yields the same size and, within one mtime tick, the
			// same mtime, which the scanner correctly treats as unchanged.
			var cuts []int
			for n, b := range content {
				if b == '\n' && n+1 < len(content) {
					cuts = append(cuts, n+1)
				}
			}
			rng := rand.New(rand.NewSource(17))
			for i := 0; i < 15; i++ {
				cuts = append(cuts, cuts[rng.Intn(len(cuts))])
			}
			for _, cut := range cuts {
				root := t.TempDir()
				project := filepath.Join(root, "projects", "p")
				if err := os.MkdirAll(project, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(project, name+".jsonl")
				if err := os.WriteFile(path, content[:cut], 0o600); err != nil {
					t.Fatal(err)
				}
				p := New(root, nil)
				prefix, err := p.Scan(context.Background(), provider.ScanState{})
				if err != nil {
					t.Fatal(err)
				}
				if prefix.State.Sources[path].Offset != int64(cut) {
					t.Fatalf("offset at %d = %d", cut, prefix.State.Sources[path].Offset)
				}
				if err := os.WriteFile(path, content, 0o600); err != nil {
					t.Fatal(err)
				}
				resumed, err := p.Scan(context.Background(), prefix.State)
				if err != nil || len(resumed.Changed) != 1 {
					t.Fatalf("resumed at %d: %+v, %v", cut, resumed.Changed, err)
				}
				want := baseline
				want.SourcePath = path
				if !reflect.DeepEqual(resumed.Changed[0], want) {
					t.Fatalf("resume at %d mismatch\n got: %+v\nwant: %+v", cut, resumed.Changed[0], want)
				}
			}
		})
	}
}

func TestScanIncompleteLastLine(t *testing.T) {
	t.Parallel()
	root, path := fixtureRoot(t, "partial_last_line")
	p := New(root, nil)
	prefix, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	old := prefix.State.Sources[path]
	if old.Offset >= info.Size() || prefix.Changed[0].Counts.User != 1 {
		t.Errorf("incomplete tail was consumed: offset=%d size=%d meta=%+v", old.Offset, info.Size(), prefix.Changed[0])
	}
	const rest = `lete"}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rest); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := p.Scan(context.Background(), prefix.State)
	if err != nil || len(resumed.Changed) != 1 {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	m := resumed.Changed[0]
	if m.Counts.User != 2 || m.Counts.Assistant != 1 || m.Tokens != (model.TokenUsage{Input: 10, Output: 5}) {
		t.Errorf("resume metadata = %+v", m)
	}
	full, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil || len(full.Changed) != 1 || !reflect.DeepEqual(m, full.Changed[0]) {
		t.Errorf("resume/full mismatch: %+v / %+v, %v", m, full.Changed, err)
	}
}

func TestScanDiagnosticsAndTimeFallback(t *testing.T) {
	t.Parallel()
	root, path := fixtureRoot(t, "unknown_types")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{bad json}\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := New(root, nil).Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Diag.ParseErrors != 1 || !reflect.DeepEqual(result.Diag.UnknownTypes, map[string]int{"future-widget": 2, "unknown-action": 1}) || len(result.Diag.Warnings) != 1 || result.Diag.Warnings[0].Line != 6 || result.Diag.Warnings[0].Path != path {
		t.Errorf("diagnostics = %+v", result.Diag)
	}
	var cp checkpoint
	modTime := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if got := cp.finalize(modTime).UpdatedAt; !got.Equal(modTime) {
		t.Errorf("mtime fallback = %s", got)
	}
}

func TestScanCheckpointJSONRoundtrip(t *testing.T) {
	t.Parallel()
	root, path := fixtureRoot(t, "basic")
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	var old checkpoint
	if err := json.Unmarshal(first.State.Sources[path].Checkpoint, &old); err != nil {
		t.Fatal(err)
	}
	if old.Version != checkpointVersion || old.Meta.Tokens != (model.TokenUsage{Input: 100, Output: 35, CacheRead: 30, CacheWrite: 10}) || old.PendingUsage != (model.TokenUsage{Input: 150, Output: 40}) || old.LastAssistantID != "msg-a2" {
		t.Errorf("checkpoint = %+v", old)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(strings.Join([]string{
		`{"type":"assistant","timestamp":"2026-09-20T10:02:00Z","message":{"id":"msg-a2","content":[{"type":"tool_use"}],"usage":{"input_tokens":150,"output_tokens":49}}}`,
		`{"type":"assistant","timestamp":"2026-09-20T10:02:01Z","message":{"id":"msg-a3","content":[],"usage":{"input_tokens":20,"output_tokens":4}}}`,
	}, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
	resumed, err := p.Scan(context.Background(), first.State)
	if err != nil || len(resumed.Changed) != 1 {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	full, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil || len(full.Changed) != 1 || !reflect.DeepEqual(resumed.Changed, full.Changed) {
		t.Errorf("resume/full mismatch: %+v / %+v, %v", resumed.Changed, full.Changed, err)
	}
	if resumed.Changed[0].Counts.Assistant != 3 || resumed.Changed[0].Counts.ToolCalls != 2 || resumed.Changed[0].Tokens != (model.TokenUsage{Input: 270, Output: 88, CacheRead: 30, CacheWrite: 10}) {
		t.Errorf("updated counts/tokens = %+v", resumed.Changed[0])
	}
}

func TestScanTurnState(t *testing.T) {
	t.Parallel()
	const (
		prompt     = `{"type":"user","message":{"content":"Fix the bug"}}`
		thinking   = `{"type":"assistant","message":{"id":"a1","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"..."}]}}`
		call       = `{"type":"assistant","message":{"id":"a1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash"}]}}`
		call2      = `{"type":"assistant","message":{"id":"a1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t2","name":"Read"}]}}`
		result1    = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`
		result2    = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}`
		answer     = `{"type":"assistant","message":{"id":"a2","stop_reason":"end_turn","content":[{"type":"text","text":"Fixed."}]}}`
		streamText = `{"type":"assistant","message":{"id":"a2","stop_reason":null,"content":[{"type":"text","text":"Fixed."}]}}`
		streamCall = `{"type":"assistant","message":{"id":"a2","stop_reason":null,"content":[{"type":"tool_use","id":"t3"}]}}`
		interrupt  = `{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
		apiError   = `{"type":"assistant","isApiErrorMessage":true,"message":{"content":[{"type":"text","text":"API Error"}]}}`
		sidechain  = `{"type":"assistant","isSidechain":true,"message":{"id":"s1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"s9"}]}}`
		metaUser   = `{"type":"user","isMeta":true,"message":{"content":"Caveat: local command"}}`
		command    = `{"type":"user","message":{"content":"<command-name>/model</command-name>"}}`
	)
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"no conversation", nil, ""},
		{"prompt", []string{prompt}, model.TurnWorking},
		{"thinking before a call", []string{prompt, thinking}, model.TurnWorking},
		{"call waits", []string{prompt, call}, model.TurnTool},
		{"parallel calls wait for both", []string{prompt, call, call2, result1}, model.TurnTool},
		{"results answered", []string{prompt, call, call2, result2, result1}, model.TurnWorking},
		{"end turn", []string{prompt, call, result1, answer}, model.TurnDone},
		{"older text without stop reason", []string{prompt, streamText}, model.TurnDone},
		{"older call without stop reason", []string{prompt, streamText, streamCall}, model.TurnTool},
		{"interrupted at a call", []string{prompt, call, interrupt}, model.TurnDone},
		{"API error", []string{prompt, apiError}, model.TurnDone},
		{"a new prompt drops an abandoned call", []string{prompt, call, prompt}, model.TurnWorking},
		{"sidechain and meta records leave it", []string{prompt, call, result1, answer, sidechain, metaUser, command}, model.TurnDone},
	}
	for _, tt := range tests {
		var cp checkpoint
		var d provider.Diagnostics
		for _, line := range tt.lines {
			cp.consume([]byte(line), &d)
		}
		if got := cp.finalize(time.Time{}).Turn; got != tt.want {
			t.Errorf("%s: Turn = %q, want %q", tt.name, got, tt.want)
		}
	}

	// Incremental scans carry pending calls across appends.
	root := t.TempDir()
	project := filepath.Join(root, "projects", "p")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "s.jsonl")
	p := New(root, nil)
	state := provider.ScanState{}
	for i, step := range []struct {
		lines []string
		want  string
	}{
		{[]string{prompt, call, call2}, model.TurnTool},
		{[]string{result1}, model.TurnTool},
		{[]string{result2}, model.TurnWorking},
		{[]string{answer}, model.TurnDone},
	} {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(strings.Join(step.lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		result, err := p.Scan(context.Background(), state)
		if err != nil || len(result.Changed) != 1 {
			t.Fatalf("step %d: %+v, %v", i, result.Changed, err)
		}
		if got := result.Changed[0].Turn; got != step.want {
			t.Errorf("step %d: Turn = %q, want %q", i, got, step.want)
		}
		state = result.State
	}
}
