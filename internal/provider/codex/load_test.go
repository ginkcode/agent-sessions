package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func syntheticRollout(t *testing.T, lines ...string) (*Provider, model.SessionRef, string) {
	t.Helper()
	root := platform.TempDir(t)
	path := filepath.Join(root, "sessions", "2026", "09", "28", "rollout-2026-09-28T12-00-00-fixture.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return New(root, nil), model.SessionRef{Agent: model.AgentCodex, ID: "fixture"}, path
}

func record(kind, payload string) string {
	return fmt.Sprintf(`{"timestamp":"2026-09-28T12:00:00Z","type":%q,"payload":%s}`+"\n", kind, payload)
}

func TestLoadSyntheticConversation(t *testing.T) {
	p, ref, path := syntheticRollout(t,
		record("session_meta", `{"id":"fixture","cwd":"/nonexistent/synthetic","timestamp":"2026-09-28T11:00:00Z","cli_version":"1.2"}`),
		record("turn_context", `{"model":"gpt-fixture"}`),
		record("response_item", `{"type":"message","role":"developer","content":[{"type":"input_text","text":"instructions"}]}`),
		record("response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>injected"}]}`),
		record("event_msg", `{"type":"user_message","message":"Human question"}`),
		record("response_item", `{"type":"message","role":"user","content":[{"type":"input_text","text":"Human question"}]}`),
		record("response_item", `{"type":"reasoning","summary":[{"text":"thinking summary"}],"encrypted_content":"SECRET_REASONING"}`),
		record("response_item", `{"type":"function_call","call_id":"call-1","name":"shell","arguments":{"big":9007199254740993}}`),
		record("event_msg", `{"type":"agent_message","message":"Answer"}`),
		record("response_item", `{"type":"function_call_output","call_id":"call-1","output":"ok"}`),
		record("response_item", `{"type":"message","id":"answer-1","role":"assistant","content":[{"type":"output_text","text":"Answer"}]}`),
		record("response_item", `{"type":"message","id":"answer-1","role":"assistant","content":[{"type":"output_text","text":"more"}]}`),
		record("response_item", `{"type":"custom_tool_call","call_id":"call-2","name":"apply_patch","input":{"nested":7}}`),
		record("response_item", `{"type":"custom_tool_call_output","call_id":"call-2","output":"patched","status":"failed"}`),
		record("response_item", `{"type":"function_call_output","call_id":"unknown","output":"not linked"}`),
		record("compacted", `{"message":"short summary","replacement_history":[{"type":"message","content":"DO_NOT_EXPAND"}]}`),
		record("event_msg", `{"type":"turn_aborted","message":"stopped"}`),
		record("event_msg", `{"type":"token_count","info":{"total_token_usage":{"input_tokens":42,"output_tokens":7}}}`),
		record("response_item", `{"type":"future_type","text":"unknown"}`),
		"not json\n",
	)
	ctx := context.Background()
	scan, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Changed) != 1 || !reflect.DeepEqual(transcript.Meta, scan.Changed[0]) {
		t.Fatalf("Load.Meta %+v != Scan.Meta %+v", transcript.Meta, scan.Changed)
	}
	if transcript.Meta.SourcePath != path || transcript.Meta.Counts != (model.MessageCounts{User: 1, Assistant: 1, ToolCalls: 2}) {
		t.Errorf("meta = %+v", transcript.Meta)
	}
	if transcript.Meta.Tokens.Input != 42 || transcript.Meta.Tokens.Output != 7 {
		t.Errorf("tokens = %+v", transcript.Meta.Tokens)
	}
	msgs := transcript.Messages
	if len(msgs) != 8 {
		t.Fatalf("messages = %d, want 8: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != model.RoleSystem || !msgs[0].IsMeta || msgs[1].Role != model.RoleUser || !msgs[1].IsMeta {
		t.Errorf("context/developer meta flags = %+v / %+v", msgs[0], msgs[1])
	}
	if msgs[2].Role != model.RoleUser || msgs[2].IsMeta || msgs[2].Model != "gpt-fixture" {
		t.Errorf("user = %+v", msgs[2])
	}
	if msgs[3].Role != model.RoleAssistant || len(msgs[3].Parts) != 2 || msgs[3].Parts[0].Kind != model.PartReasoning {
		t.Fatalf("reasoning and tool = %+v", msgs[3])
	}
	call1 := msgs[3].Parts[1].Tool
	if call1 == nil || call1.Status != model.ToolCompleted || call1.Output != "ok" || string(call1.Input) != `{"big":9007199254740993}` {
		t.Errorf("paired call = %+v", call1)
	}
	if msgs[4].ID != "answer-1" || len(msgs[4].Parts) != 3 || msgs[4].Parts[0].Text != "Answer" {
		t.Errorf("assistant merge = %+v", msgs[4])
	}
	if msgs[4].Parts[2].Tool == nil || msgs[4].Parts[2].Tool.Status != model.ToolError {
		t.Errorf("custom tool = %+v", msgs[4])
	}
	if msgs[5].Parts[0].Kind != model.PartNotice || msgs[6].Parts[0].Kind != model.PartCompaction || msgs[7].Parts[0].Kind != model.PartNotice {
		t.Errorf("unmatched output/compaction/abort = %+v", msgs[5:])
	}
	serialized, _ := json.Marshal(transcript)
	for _, secret := range []string{"SECRET_REASONING", "DO_NOT_EXPAND"} {
		if strings.Contains(string(serialized), secret) {
			t.Errorf("transcript contains %q", secret)
		}
	}
	_, diag, err := p.loadRollout(ctx, transcript.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if diag.ParseErrors != 1 || diag.UnknownTypes["response_item:future_type"] != 1 || len(diag.Warnings) < 2 {
		t.Errorf("diagnostics = %+v", diag)
	}
}

func TestLoadLazyOutputAndAttachments(t *testing.T) {
	large := strings.Repeat("多", 35000)
	p, ref, path := syntheticRollout(t,
		record("session_meta", `{"id":"fixture","cwd":"/tmp"}`),
		record("response_item", `{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,UE5H"},{"type":"input_file","filename":"note.txt","file_data":"data:text/plain;base64,SEVMTE8="}]}`),
		record("response_item", `{"type":"function_call","call_id":"large","name":"run","arguments":"{}"}`),
		record("response_item", fmt.Sprintf(`{"type":"function_call_output","call_id":"large","output":%q}`, large)),
	)
	tr, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Messages) != 2 || len(tr.Messages[0].Parts) != 2 {
		t.Fatalf("messages = %+v", tr.Messages)
	}
	for i, want := range []string{"PNG", "HELLO"} {
		f := tr.Messages[0].Parts[i].File
		if f == nil || f.Ref == "" {
			t.Fatalf("file %d = %+v", i, f)
		}
		data, err := p.Blob(context.Background(), ref, f.Ref)
		if err != nil || string(data) != want {
			t.Errorf("Blob(%q) = %q, %v", f.Ref, data, err)
		}
	}
	call := tr.Messages[1].Parts[0].Tool
	if call == nil || !call.OutputTruncated || len(call.Output) > maxOutputBytes || call.OutputRef == "" {
		t.Fatalf("large tool = %+v", call)
	}
	full, err := p.Blob(context.Background(), ref, call.OutputRef)
	if err != nil || string(full) != large {
		t.Errorf("large Blob len=%d err=%v", len(full), err)
	}
	encoded, _ := json.Marshal(tr)
	if strings.Contains(string(encoded), "data:image") || strings.Contains(string(encoded), "UE5H") || strings.Contains(string(encoded), "SEVMTE8=") || strings.Contains(string(encoded), large) {
		t.Fatal("attachment data or full output leaked into transcript")
	}
	if path != tr.Meta.SourcePath {
		t.Errorf("source = %q", tr.Meta.SourcePath)
	}
	for _, key := range []string{"file:../secret", "rec:-1:large", "rec:0:", "rec:0:not-a-call", "rec:0:0", "rec:100000000000000000000000:large", "rec:1:large"} {
		if _, err := p.Blob(context.Background(), ref, key); err == nil {
			t.Errorf("Blob(%q) accepted invalid key", key)
		}
	}
}

func TestLoadPartialTailAndTenMegabyteLine(t *testing.T) {
	large := strings.Repeat("x", 10<<20)
	p, ref, path := syntheticRollout(t,
		record("session_meta", `{"id":"fixture"}`),
		record("response_item", fmt.Sprintf(`{"type":"function_call","call_id":"big","name":"run","arguments":%q}`, large)),
		`{"timestamp":"2026-09-28T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"tail"}]}}`,
	)
	scan, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if scan.Changed[0].Counts.User != 0 || scan.State.Sources[path].Offset == int64(len(mustRead(t, path))) {
		t.Errorf("scan committed partial line: %+v", scan)
	}
	tr, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tr.Meta, scan.Changed[0]) {
		t.Error("Load.Meta diverges from Scan.Meta for partial tail")
	}
	if len(tr.Messages) != 2 || tr.Messages[1].Parts[0].Text != "tail" {
		t.Errorf("messages = %+v", tr.Messages)
	}
	if len(tr.Messages[0].Parts[0].Tool.Input) != len(large)+2 {
		t.Errorf("large input length = %d", len(tr.Messages[0].Parts[0].Tool.Input))
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLoadIndexPathValidationAndCancellation(t *testing.T) {
	p, ref, _ := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`))
	bad := model.SessionRef{Agent: model.AgentCodex, ID: "../auth.json"}
	if _, err := p.Load(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("bad ref: %v", err)
	}
	if _, err := p.Load(context.Background(), model.SessionRef{Agent: model.AgentOpenCode, ID: ref.ID}); err == nil {
		t.Error("wrong agent accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Load(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Load cancellation: %v", err)
	}
	if _, err := p.Blob(ctx, ref, "rec:0:0"); !errors.Is(err, context.Canceled) {
		t.Errorf("Blob cancellation: %v", err)
	}
	// A DB row pointing to an outside root must not become a Blob/Load source.
	outside := filepath.Join(platform.TempDir(t), "rollout-outside.jsonl")
	if err := os.WriteFile(outside, []byte(record("session_meta", `{"id":"outside"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(p.root, "state_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT); INSERT INTO threads VALUES ('outside', '` + outside + `');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(context.Background(), model.SessionRef{Agent: model.AgentCodex, ID: "outside"}); err == nil {
		t.Error("outside index row accepted")
	}
}
