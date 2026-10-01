package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/tidwall/gjson"
)

// writeRollout writes a rollout with content under root/sessions/<date>/ and
// returns its path.
func writeRollout(t *testing.T, root, uuid, content string, archived bool) string {
	t.Helper()
	var dir string
	if archived {
		dir = filepath.Join(root, "archived_sessions")
	} else {
		dir = filepath.Join(root, "sessions", "2026", "09", "28")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-28T12-00-00-"+uuid+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(ts, kind, payload string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":%q,"payload":%s}`+"\n", ts, kind, payload)
}

// fixtureLines builds a session with 2 user prompts (one injected context) and
// 3 assistant replies, 4 tool calls (one duplicate ID), token events and
// duplicate event messages.
func fixtureLines() []string {
	const id = "11111111-1111-1111-1111-111111111111"
	return []string{
		line("2026-09-28T12:00:00Z", "session_meta",
			`{"id":"`+id+`","timestamp":"2026-09-28T12:00:00Z","cwd":"/tmp/proj","cli_version":"0.156.1","git":{"branch":"main"}}`),
		line("2026-09-28T12:00:01Z", "turn_context", `{"cwd":"/tmp/proj","model":"gpt-5-codex"}`),
		// Injected context (not counted) and the real first prompt.
		line("2026-09-28T12:00:02Z", "response_item",
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"<user_instructions> injected"}]}`),
		line("2026-09-28T12:00:03Z", "response_item",
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"First  real\nprompt"}]}`),
		line("2026-09-28T12:00:04Z", "response_item",
			`{"type":"message","role":"assistant","id":"msg_1","content":[{"type":"output_text","text":"Reply one."}]}`),
		line("2026-09-28T12:00:05Z", "event_msg", `{"type":"user_message","message":"First  real\nprompt"}`),
		line("2026-09-28T12:00:06Z", "event_msg", `{"type":"agent_message","message":"Reply one."}`),
		line("2026-09-28T12:00:07Z", "response_item",
			`{"type":"function_call","name":"shell","arguments":"{\"cmd\":[\"ls\"]}","call_id":"call_1"}`),
		line("2026-09-28T12:00:08Z", "response_item",
			`{"type":"function_call_output","call_id":"call_1","output":"file-a\nfile-b"}`),
		line("2026-09-28T12:00:09Z", "response_item",
			`{"type":"function_call","name":"shell","arguments":"{}","call_id":"call_2"}`),
		line("2026-09-28T12:00:10Z", "event_msg",
			`{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":30,"reasoning_output_tokens":5,"total_tokens":47}}}`),
		// Duplicate assistant ID is not counted twice.
		line("2026-09-28T12:00:11Z", "response_item",
			`{"type":"message","role":"assistant","id":"msg_1","content":[{"type":"output_text","text":"Reply one."}]}`),
		line("2026-09-28T12:00:12Z", "response_item",
			`{"type":"message","role":"assistant","id":"msg_2","content":[{"type":"output_text","text":"Reply two."}]}`),
		line("2026-09-28T12:00:13Z", "response_item",
			`{"type":"function_call","name":"update_plan","arguments":"[]","call_id":"call_2"}`),
		line("2026-09-28T12:00:14Z", "response_item",
			`{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch","call_id":"call_3"}`),
		line("2026-09-28T12:00:15Z", "event_msg",
			`{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":300,"reasoning_output_tokens":50,"total_tokens":470}}}`),
		line("2026-09-28T12:00:16Z", "compacted", `{"message":"compaction summary"}`),
		// Later timestamped unknown rollout variant must be skipped via diag.
		line("2026-09-28T12:00:17Z", "world_state", `{"snapshot":"ignored"}`),
	}
}

func basicFixture() string {
	return strings.Join(fixtureLines(), "")
}

func writeBasic(t *testing.T, root string) string {
	return writeRollout(t, root, "11111111-1111-1111-1111-111111111111", basicFixture(), false)
}

func TestScan_FixtureMeta(t *testing.T) {
	root := t.TempDir()
	writeBasic(t, root)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1: %+v", len(res.Changed), res.Changed)
	}
	meta := res.Changed[0]
	if meta.Ref != (model.SessionRef{Agent: model.AgentCodex, ID: "11111111-1111-1111-1111-111111111111"}) {
		t.Errorf("Ref = %+v", meta.Ref)
	}
	if meta.SourcePath == "" || !strings.HasSuffix(meta.SourcePath, ".jsonl") {
		t.Errorf("SourcePath = %q", meta.SourcePath)
	}
	if meta.CWD != "/tmp/proj" {
		t.Errorf("CWD = %q, want /tmp/proj", meta.CWD)
	}
	if meta.AgentVersion != "0.156.1" {
		t.Errorf("AgentVersion = %q", meta.AgentVersion)
	}
	if meta.GitBranch != "main" {
		t.Errorf("GitBranch = %q", meta.GitBranch)
	}
	if meta.Model != "gpt-5-codex" {
		t.Errorf("Model = %q", meta.Model)
	}
	if meta.Title != "First real prompt" {
		t.Errorf("Title = %q, want first prompt on one line", meta.Title)
	}
	if meta.FirstPrompt != "First real prompt" {
		t.Errorf("FirstPrompt = %q", meta.FirstPrompt)
	}
	if meta.Counts.User != 1 || meta.Counts.Assistant != 2 || meta.Counts.ToolCalls != 3 {
		t.Errorf("Counts = %+v, want user=1 assistant=2 toolCalls=3", meta.Counts)
	}
	want := model.TokenUsage{Input: 100, Output: 300, Reasoning: 50, CacheRead: 20}
	if meta.Tokens != want {
		t.Errorf("Tokens = %+v, want %+v (latest cumulative, not summed)", meta.Tokens, want)
	}
	if meta.CreatedAt != time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) {
		t.Errorf("CreatedAt = %v", meta.CreatedAt)
	}
	if meta.UpdatedAt != time.Date(2026, 9, 28, 12, 0, 17, 0, time.UTC) {
		t.Errorf("UpdatedAt = %v", meta.UpdatedAt)
	}
	if meta.Archived {
		t.Errorf("Archived = true for active rollout")
	}
	if len(res.Diag.UnknownTypes) != 0 {
		t.Errorf("UnknownTypes = %+v, want empty (known non-conversational types)", res.Diag.UnknownTypes)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed = %v, want none", res.Removed)
	}
}

func TestScan_UnchangedSkipsAndIncrementalAppend(t *testing.T) {
	root := t.TempDir()
	path := writeBasic(t, root)
	p := New(root, nil)
	ctx := context.Background()

	first, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}

	// Unchanged size and mtime: no Changed entries, state retained.
	second, err := p.Scan(ctx, first.State)
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if len(second.Changed) != 0 || len(second.Removed) != 0 {
		t.Errorf("unchanged Scan Changed=%d Removed=%d, want 0/0", len(second.Changed), len(second.Removed))
	}

	// Appended data: resume from offset; result must equal a full rescan.
	appendLines := []string{
		line("2026-09-28T13:00:00Z", "response_item",
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"Follow-up"}]}`),
		line("2026-09-28T13:00:01Z", "response_item",
			`{"type":"message","role":"assistant","id":"msg_3","content":[{"type":"output_text","text":"Done."}]}`),
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Join(appendLines, "")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	newTime := time.Date(2026, 9, 28, 13, 0, 2, 0, time.UTC)
	if err := os.Chtimes(path, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	resumed, err := p.Scan(ctx, first.State)
	if err != nil {
		t.Fatalf("resumed Scan: %v", err)
	}
	if len(resumed.Changed) != 1 {
		t.Fatalf("resumed Changed = %d, want 1", len(resumed.Changed))
	}

	full, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatalf("full rescan: %v", err)
	}
	if len(full.Changed) != 1 {
		t.Fatalf("full Changed = %d, want 1", len(full.Changed))
	}
	got, want := resumed.Changed[0], full.Changed[0]
	if got.Counts != want.Counts || got.Tokens != want.Tokens || got.Title != want.Title {
		t.Errorf("resumed meta differs from full scan:\n got %+v\nwant %+v", got, want)
	}
	if got.Counts.User != 2 || got.Counts.Assistant != 3 || got.Counts.ToolCalls != 3 {
		t.Errorf("resumed Counts = %+v", got.Counts)
	}
	if got.Title != "Follow-up" || got.FirstPrompt != "First real prompt" {
		t.Errorf("resumed title/first prompt = %q / %q", got.Title, got.FirstPrompt)
	}
	if got.UpdatedAt != want.UpdatedAt || got.CreatedAt != want.CreatedAt {
		t.Errorf("resumed times differ: got %v/%v want %v/%v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}

	// Exact append resume: state offset advanced, counts carried over.
	if resumed.State.Sources[path].Offset != full.State.Sources[path].Offset {
		t.Errorf("resumed offset %d != full %d", resumed.State.Sources[path].Offset, full.State.Sources[path].Offset)
	}
	if resumed.State.Sources[path].Size != full.State.Sources[path].Size {
		t.Errorf("resumed size %d != full %d", resumed.State.Sources[path].Size, full.State.Sources[path].Size)
	}
}

func TestScan_PartialFinalLine(t *testing.T) {
	root := t.TempDir()
	path := writeBasic(t, root)

	// Truncated tail without newline: must not be consumed into the checkpoint.
	complete := basicFixture()
	partial := complete[:len(complete)-40]
	if err := os.WriteFile(path, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	partialRes, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan with partial tail: %v", err)
	}
	if len(partialRes.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(partialRes.Changed))
	}
	partialMeta := partialRes.Changed[0]
	partialOffset := partialRes.State.Sources[path].Offset
	if partialOffset == 0 || partialOffset >= int64(len(partial)) {
		t.Errorf("partial offset = %d, file len = %d", partialOffset, len(partial))
	}

	// Append the missing tail: next scan picks up exactly the remaining record.
	if err := os.WriteFile(path, []byte(complete), 0o644); err != nil {
		t.Fatal(err)
	}
	newTime := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	completedRes, err := p.Scan(context.Background(), partialRes.State)
	if err != nil {
		t.Fatalf("Scan after completing tail: %v", err)
	}
	if len(completedRes.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(completedRes.Changed))
	}
	full, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("full rescan: %v", err)
	}
	a, b := completedRes.Changed[0], full.Changed[0]
	if a.Counts != b.Counts || a.Tokens != b.Tokens || a.Title != b.Title {
		t.Errorf("completed partial meta differs from full scan:\n got %+v\nwant %+v", a, b)
	}
	if partialMeta.Counts.Assistant != 2 {
		t.Errorf("partial Counts.Assistant = %d, want 2 (complete records only)", partialMeta.Counts.Assistant)
	}
	if partialMeta.Counts.ToolCalls != 3 {
		t.Errorf("partial Counts.ToolCalls = %d, want 3 (complete records only)", partialMeta.Counts.ToolCalls)
	}
}

func TestScan_ShrinkRescansFully(t *testing.T) {
	root := t.TempDir()
	path := writeBasic(t, root)
	p := New(root, nil)
	ctx := context.Background()

	first, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	shrunk := strings.Join(fixtureLines()[:8], "")
	if err := os.WriteFile(path, []byte(shrunk), 0o644); err != nil {
		t.Fatal(err)
	}
	newTime := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	res, err := p.Scan(ctx, first.State)
	if err != nil {
		t.Fatalf("Scan after shrink: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(res.Changed))
	}
	// 8 lines = meta, turn_context, 2 user (1 injected), assistant, 2 events,
	// then the first function_call.
	meta := res.Changed[0]
	if meta.Counts.User != 1 || meta.Counts.Assistant != 1 || meta.Counts.ToolCalls != 1 {
		t.Errorf("Counts after shrink = %+v, want user=1 assistant=1 tools=1", meta.Counts)
	}
}

func TestScan_Removal(t *testing.T) {
	root := t.TempDir()
	writeBasic(t, root)
	p := New(root, nil)
	ctx := context.Background()

	first, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if len(first.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(first.Changed))
	}
	if err := os.Remove(first.Changed[0].SourcePath); err != nil {
		t.Fatal(err)
	}
	res, err := p.Scan(ctx, first.State)
	if err != nil {
		t.Fatalf("Scan after removal: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != first.Changed[0].Ref {
		t.Errorf("Removed = %+v, want [%+v]", res.Removed, first.Changed[0].Ref)
	}
	if len(res.Changed) != 0 {
		t.Errorf("Changed = %d, want 0", len(res.Changed))
	}
}

func TestScan_ArchivedRollout(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root, "33333333-3333-3333-3333-333333333333",
		line("2026-09-20T08:15:00Z", "session_meta",
			`{"id":"33333333-3333-3333-3333-333333333333","cwd":"/tmp/proj","cli_version":"0.156.1"}`), true)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(res.Changed))
	}
	if !res.Changed[0].Archived {
		t.Errorf("Archived = false for archived_sessions rollout")
	}
}

func TestScan_ActivePreferredOverArchivedDuplicate(t *testing.T) {
	root := t.TempDir()
	const id = "44444444-4444-4444-4444-444444444444"
	content := line("2026-09-20T08:15:00Z", "session_meta", `{"id":"`+id+`","cwd":"/tmp/proj"}`) +
		line("2026-09-20T08:16:00Z", "response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"Active"}]}`)
	writeRollout(t, root, id, content, false)
	writeRollout(t, root, id, content, true)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1 for duplicate ID across active/archived", len(res.Changed))
	}
	if res.Changed[0].Archived {
		t.Errorf("active rollout should win over archived duplicate")
	}
	if res.Changed[0].SourcePath == "" || strings.Contains(res.Changed[0].SourcePath, "archived_sessions") {
		t.Errorf("SourcePath = %q, want active path", res.Changed[0].SourcePath)
	}
}

func TestScan_MissingSessionIDFallsBackToFilename(t *testing.T) {
	root := t.TempDir()
	content := line("2026-09-28T12:00:00Z", "response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`)
	writeRollout(t, root, "55555555-5555-5555-5555-555555555555", content, false)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(res.Changed))
	}
	if res.Changed[0].Ref.ID != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("Ref.ID = %q, want filename UUID fallback", res.Changed[0].Ref.ID)
	}
}

func TestScan_MalformedLinesAreDiagnostics(t *testing.T) {
	root := t.TempDir()
	content := line("2026-09-28T12:00:00Z", "session_meta", `{"id":"66666666-6666-6666-6666-666666666666","cwd":"/tmp"}`) +
		"{not json}\n" +
		line("2026-09-28T12:00:01Z", "response_item", `{"type":"message"}`) + "\n" +
		line("2026-09-28T12:00:02Z", "unknown_kind", `{}`) + "\n" +
		line("2026-09-28T12:00:03Z", "response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"after"}]}`) + "\n"
	writeRollout(t, root, "66666666-6666-6666-6666-666666666666", content, false)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(res.Changed))
	}
	if res.Changed[0].Counts.User != 1 {
		t.Errorf("Counts.User = %d, want 1 (scan continues past bad lines)", res.Changed[0].Counts.User)
	}
	if res.Diag.ParseErrors < 2 {
		t.Errorf("ParseErrors = %d, want >= 2", res.Diag.ParseErrors)
	}
	if res.Diag.UnknownTypes["unknown_kind"] != 1 {
		t.Errorf("UnknownTypes = %+v, want unknown_kind=1", res.Diag.UnknownTypes)
	}
}

func TestScan_EmptyRootNoRemovals(t *testing.T) {
	root := t.TempDir()
	p := New(root, nil)
	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan on empty root: %v", err)
	}
	if len(res.Changed) != 0 || len(res.Removed) != 0 {
		t.Errorf("Scan on empty root = %+v, want empty", res)
	}
}

func TestScan_CanceledContext(t *testing.T) {
	root := t.TempDir()
	writeBasic(t, root)
	p := New(root, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Scan(ctx, provider.ScanState{}); err == nil {
		t.Fatal("Scan on canceled context returned nil error")
	}
}

// A cancelled scan must not commit state, so a later scan sees all sessions.
func TestScan_CanceledScanDoesNotCorruptState(t *testing.T) {
	root := t.TempDir()
	writeBasic(t, root)
	p := New(root, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Scan(ctx, provider.ScanState{}); err == nil {
		t.Fatal("expected error on canceled context")
	}
	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan after cancel: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Errorf("Changed after canceled attempt = %d, want 1", len(res.Changed))
	}
}

func TestScan_RobustUnknownTypes(t *testing.T) {
	root := t.TempDir()
	content := line("2026-09-28T12:00:00Z", "session_meta", `{"id":"77777777-7777-7777-7777-777777777777","cwd":"/tmp"}`) +
		line("2026-09-28T12:00:01Z", "inter_agent_communication", `{}`) +
		line("2026-09-28T12:00:02Z", "token_usage_record", `{}`) +
		line("2026-09-28T12:00:03Z", "world_state", `{}`) +
		line("2026-09-28T12:00:04Z", "security_risk_score", `{}`) +
		line("2026-09-28T12:00:05Z", "realtime_item", `{}`) +
		line("2026-09-28T12:00:06Z", "retained_context", `{}`) +
		line("2026-09-28T12:00:07Z", "response_item", `{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]}`) +
		line("2026-09-28T12:00:07Z", "response_item", `{"type":"local_shell_call","call_id":"shell_1","action":{"command":["ls"]}}`)
	writeRollout(t, root, "77777777-7777-7777-7777-777777777777", content, false)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Diag.UnknownTypes) != 0 {
		t.Errorf("UnknownTypes = %+v, want none (known non-conversational types)", res.Diag.UnknownTypes)
	}
	meta := res.Changed[0]
	if meta.Counts.User != 0 || meta.Counts.Assistant != 0 || meta.Counts.ToolCalls != 1 {
		t.Errorf("Counts = %+v, want only the local_shell_call tool", meta.Counts)
	}
}

func TestScan_DeveloperRoleNotCounted(t *testing.T) {
	root := t.TempDir()
	content := line("2026-09-28T12:00:00Z", "session_meta", `{"id":"88888888-8888-8888-8888-888888888888","cwd":"/tmp"}`) +
		line("2026-09-28T12:00:01Z", "response_item", `{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev instructions"}]}`) +
		line("2026-09-28T12:00:02Z", "response_item", `{"type":"message","role":"system","content":[{"type":"input_text","text":"sys"}]}`)
	writeRollout(t, root, "88888888-8888-8888-8888-888888888888", content, false)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Diag.UnknownTypes) != 0 {
		t.Errorf("UnknownTypes = %+v, want none", res.Diag.UnknownTypes)
	}
	if res.Changed[0].Counts.User != 0 || res.Changed[0].Counts.Assistant != 0 {
		t.Errorf("Counts = %+v, want no conversation counts", res.Changed[0].Counts)
	}
}

func TestScan_CompactedNotCounted(t *testing.T) {
	root := t.TempDir()
	content := line("2026-09-28T12:00:00Z", "session_meta", `{"id":"99999999-9999-9999-9999-999999999999","cwd":"/tmp"}`) +
		line("2026-09-28T12:00:01Z", "compacted", `{"message":"summary","replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"replacement user"}]}]}`)
	writeRollout(t, root, "99999999-9999-9999-9999-999999999999", content, false)
	p := New(root, nil)

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Changed[0].Counts.User != 0 || res.Changed[0].Counts.Assistant != 0 {
		t.Errorf("Counts = %+v, replacement_history must not count", res.Changed[0].Counts)
	}
}

func TestTitleSkipsAttachmentOnlyLatestPrompt(t *testing.T) {
	var cp checkpoint
	for _, content := range []string{
		`[{"type":"input_text","text":"opening prompt"}]`,
		`[{"type":"input_text","text":"latest text prompt"}]`,
		`[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]`,
	} {
		cp.handleMessage("user", gjson.Parse(content), "", nil)
	}
	meta := cp.finalize("", time.Time{})
	if meta.Title != "latest text prompt" || meta.FirstPrompt != "opening prompt" || meta.Counts.User != 3 {
		t.Errorf("meta = %+v", meta)
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeBasic(t, root)
	p := New(root, nil)
	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(res.Changed))
	}
	path := res.Changed[0].SourcePath
	var cp checkpoint
	if err := json.Unmarshal(res.State.Sources[path].Checkpoint, &cp); err != nil {
		t.Fatalf("unmarshal checkpoint: %v", err)
	}
	if cp.Version != checkpointVersion {
		t.Errorf("checkpoint version = %d, want %d", cp.Version, checkpointVersion)
	}
	if cp.Meta.Ref.ID != res.Changed[0].Ref.ID {
		t.Errorf("checkpoint meta ID = %q, want %q", cp.Meta.Ref.ID, res.Changed[0].Ref.ID)
	}
	if cp.Meta.Counts != res.Changed[0].Counts {
		t.Errorf("checkpoint counts = %+v, want %+v", cp.Meta.Counts, res.Changed[0].Counts)
	}
	if !cp.FirstUserSeen {
		t.Error("FirstUserSeen not persisted")
	}
}

func TestIdFromRolloutPath(t *testing.T) {
	cases := []struct{ name, want string }{
		{"rollout-2026-09-28T12-00-00-11111111-1111-1111-1111-111111111111.jsonl", "11111111-1111-1111-1111-111111111111"},
		{"rollout-abc123.jsonl", "abc123"},
		{"rollout-2026-09-28T12-00-00-short.jsonl", "short"},
	}
	for _, tc := range cases {
		if got := idFromRolloutPath("/x/y/" + tc.name); got != tc.want {
			t.Errorf("idFromRolloutPath(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A discovered source that becomes unreadable (chmod 0000) must produce a
// provider error rather than a false removal, and a later healthy scan must
// still see the session.
func TestScan_UnreadableSourceRetainsState(t *testing.T) {
	root := t.TempDir()
	path := writeBasic(t, root)
	p := New(root, nil)
	ctx := context.Background()

	first, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if len(first.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(first.Changed))
	}

	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("running as privileged user; chmod 0000 did not make the file unreadable")
	}

	res, err := p.Scan(ctx, first.State)
	if err == nil {
		t.Fatalf("Scan with unreadable source returned nil error: %+v", res)
	}
	if len(res.Changed) != 0 || len(res.Removed) != 0 {
		t.Errorf("failed scan Changed/Removed = %+v/%+v, want empty", res.Changed, res.Removed)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := p.Scan(ctx, first.State)
	if err != nil {
		t.Fatalf("Scan after restoring permissions: %v", err)
	}
	if len(again.Changed) != 0 {
		t.Errorf("Changed after restore = %d, want 0 (state retained)", len(again.Changed))
	}
}

// A previously scanned path replaced by a directory still exists, so removal
// cannot be proven: Scan must fail instead of reporting a false removal.
func TestScan_SourceReplacedByDirectoryFails(t *testing.T) {
	root := t.TempDir()
	path := writeBasic(t, root)
	p := New(root, nil)
	ctx := context.Background()

	first, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := p.Scan(ctx, first.State)
	if err == nil {
		t.Fatalf("Scan with path replaced by directory returned nil error: %+v", res)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed = %+v, want empty (not provably deleted)", res.Removed)
	}
}

// scanSource must error, not warn, when a discovered path cannot be inspected.
func TestScanSource_InspectionFailuresAreErrors(t *testing.T) {
	root := t.TempDir()
	p := New(root, nil)
	ctx := context.Background()

	if out := p.scanSource(ctx, filepath.Join(root, "missing.jsonl"), provider.SourceState{}, false); out.err == nil {
		t.Error("scanSource on missing path returned nil error")
	}

	dir := filepath.Join(root, "dir.jsonl")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out := p.scanSource(ctx, dir, provider.SourceState{}, false); out.err == nil {
		t.Error("scanSource on directory returned nil error")
	} else if out.state.Size != 0 || out.state.Checkpoint != nil || out.meta.Ref.Agent != "" {
		t.Errorf("scanSource on directory emitted meta/state: %+v", out)
	}
}

func TestScan_NilGitResolver(t *testing.T) {
	root := t.TempDir()
	content := line("2026-09-28T12:00:00Z", "session_meta", `{"id":"aaaa1111-1111-1111-1111-111111111111","cwd":"/tmp/definitely-missing-cwd"}`)
	writeRollout(t, root, "aaaa1111-1111-1111-1111-111111111111", content, false)
	p := New(root, nil)
	p.git = nil

	res, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Changed) != 1 {
		t.Fatalf("Changed = %d, want 1", len(res.Changed))
	}
	if !res.Changed[0].CWDMissing {
		t.Errorf("CWDMissing = false, want true")
	}
	if res.Changed[0].RepoRoot != "" {
		t.Errorf("RepoRoot = %q, want empty with nil resolver", res.Changed[0].RepoRoot)
	}
}
