package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/golden"
)

func TestLoadV2GoldenAndFiles(t *testing.T) {
	f := newScanFixture(t)
	f.project("proj1", "/repo")
	f.session("parent", nil)
	f.session("child", map[string]any{"parent_id": "parent"})
	f.session("other", nil)
	f.message("parent", "user", 1, `{"time":{"created":1700000000001},"text":"Please read","files":[{"name":"image.png","mime":"image/png","data":"cGl4ZWxzAAE="}]}`)
	f.message("parent", "user", 2, `{"files":[{"filename":"empty.txt","mime":"text/plain","data":"YWJj"}]}`)
	f.message("parent", "assistant", 3, `{"time":{"created":1700000000003},"model":{"providerID":"example","id":"large"},"tokens":{"input":5,"output":6,"reasoning":2,"cache":{"read":3,"write":4}},"content":[{"type":"reasoning","text":"considering"},{"type":"text","text":"answer"},{"type":"tool","id":"call-1","name":"task","state":{"status":"completed","input":{"number":12345678901234567890},"metadata":{"output":"tool result","sessionId":"child","parentSessionId":"parent"},"content":[{"type":"text","text":"duplicate result"}]}}],"error":{"name":"test-secret","message":"secret-should-not-leak"}}`)
	f.message("parent", "compaction", 4, `{"status":"completed","reason":"limit","summary":"short recap"}`)
	f.message("parent", "system", 5, `{"text":"system note"}`)
	f.message("parent", "synthetic", 6, `{"text":"injected"}`)
	f.message("parent", "idle", 7, `{}`)
	f.message("parent", "location-switched", 8, `{}`)
	p := New(f.root, nil)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "parent"}
	transcript, err := p.Load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Messages) != 6 {
		t.Fatalf("messages = %d, want 6", len(transcript.Messages))
	}
	if transcript.Meta.Counts != (model.MessageCounts{User: 2, Assistant: 1, ToolCalls: 1}) {
		t.Errorf("meta counts = %+v", transcript.Meta.Counts)
	}
	golden.JSON(t, "load_v2", transcript.Messages)
	file := transcript.Messages[0].Parts[1].File
	data, err := p.Blob(t.Context(), ref, file.Ref)
	if err != nil || !bytes.Equal(data, []byte("pixels\x00\x01")) {
		t.Fatalf("file Blob = %q, %v", data, err)
	}
	data, err = p.Blob(t.Context(), ref, transcript.Messages[1].Parts[0].File.Ref)
	if err != nil || string(data) != "abc" {
		t.Fatalf("pure file Blob = %q, %v", data, err)
	}
	tool := transcript.Messages[2].Parts[2].Tool
	if tool.Output != "tool result" || tool.Child == nil || tool.Child.ID != "child" {
		t.Errorf("paired tool = %+v", tool)
	}
	if string(tool.Input) != `{"number":12345678901234567890}` {
		t.Errorf("input lost integer precision: %s", tool.Input)
	}
	raw, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"cGl4ZWxzAAE=", "YWJj", "duplicate result", "secret-should-not-leak"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("transcript exposed sensitive payload %q", secret)
		}
	}
}

func TestLoadV2LargeOutputAndCompaction(t *testing.T) {
	f := newScanFixture(t)
	f.session("large", nil)
	long := strings.Repeat("é", outputBudget/2+5)
	toolRow, err := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "tool", "id": "tool-large", "name": "shell", "state": map[string]any{"status": "error", "metadata": map[string]any{"output": long, "truncated": true}}}, map[string]any{"type": "tool", "id": "tool-short", "name": "shell", "state": map[string]any{"status": "completed", "content": []any{map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "text", "text": "second"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.message("large", "assistant", 1, string(toolRow))
	compactRow, err := json.Marshal(map[string]any{"summary": long, "reason": "limit", "status": "completed"})
	if err != nil {
		t.Fatal(err)
	}
	f.message("large", "compaction", 2, string(compactRow))
	p := New(f.root, nil)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "large"}
	tr, err := p.Load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tool := tr.Messages[0].Parts[0].Tool
	if tool.Status != model.ToolError || !tool.OutputTruncated || tool.OutputRef == "" || len(tool.Output) > outputBudget {
		t.Fatalf("large tool = %+v", tool)
	}
	if !strings.HasSuffix(tool.Output, "é") {
		t.Fatal("output preview cut a UTF-8 code point")
	}
	blob, err := p.Blob(t.Context(), ref, tool.OutputRef)
	if err != nil || string(blob) != long {
		t.Fatalf("tool Blob length = %d, error = %v", len(blob), err)
	}
	if tr.Messages[0].Parts[1].Tool.Output != "first\nsecond" {
		t.Errorf("fallback output = %q", tr.Messages[0].Parts[1].Tool.Output)
	}
	compaction := tr.Messages[1]
	if compaction.Role != model.RoleSystem || !compaction.IsMeta || len(compaction.Parts) != 2 || len(compaction.Parts[0].Text) > outputBudget+50 {
		t.Fatalf("compaction summary not bounded: role %q, meta %t, parts %d", compaction.Role, compaction.IsMeta, len(compaction.Parts))
	}
	blob, err = p.Blob(t.Context(), ref, compaction.Parts[1].File.Ref)
	if err != nil || string(blob) != long {
		t.Fatalf("compaction Blob length = %d, error = %v", len(blob), err)
	}
}

func TestLoadV2MalformedUnknownAndCancellation(t *testing.T) {
	f := newScanFixture(t)
	f.session("bad", nil)
	f.message("bad", "user", 1, `{"text":`)
	f.message("bad", "mystery-sensitive-payload", 2, `{}`)
	f.message("bad", "assistant", 3, `{"content":[{"type":"mystery","text":"hidden"},null,{"type":"tool","state":{"status":"pending"}},{"type":"text","text":"valid"}]}`)
	f.message("bad", "user", 4, `{"time":{"created":1700000000004},"text":"valid"}`)
	p := New(f.root, nil)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "bad"}
	tr, err := p.Load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Messages) != 2 || tr.Messages[0].Role != model.RoleAssistant || tr.Messages[1].Time != time.UnixMilli(1700000000004).UTC() {
		t.Errorf("messages after malformed/unknown = %+v", tr.Messages)
	}
	if tr.Messages[0].Parts[0].Tool.Status != model.ToolPending {
		t.Errorf("pending tool status: %+v", tr.Messages[0].Parts[0])
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Load(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Load error = %v", err)
	}
	if _, err := p.Blob(ctx, ref, "v2:1:file:0"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Blob error = %v", err)
	}

	// The private loader is where diagnostics are collected. Public Load has
	// no diagnostics return channel in the shared provider interface.
	db, err := p.openV2(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	diag := &provider.Diagnostics{}
	_, err = p.loadV2(t.Context(), tx, "bad", model.SessionMeta{}, diag)
	if err != nil {
		t.Fatal(err)
	}
	if diag.ParseErrors != 2 || diag.UnknownTypes["mystery"] != 1 || diag.UnknownTypes["mystery-sensitive-payload"] != 1 {
		t.Fatalf("unexpected diagnostics: %+v", diag)
	}
	serialized, _ := json.Marshal(diag)
	if bytes.Contains(serialized, []byte("hidden")) || bytes.Contains(serialized, []byte("secret-should-not-leak")) {
		t.Errorf("diagnostics include raw payload: %s", serialized)
	}
}

func TestV2BlobValidation(t *testing.T) {
	f := newScanFixture(t)
	f.session("owned", nil)
	f.session("stranger", nil)
	f.message("owned", "user", 1, `{"files":[{"data":"YQ=="}]}`)
	f.message("stranger", "user", 2, `{"files":[{"data":"Yg=="}]}`)
	f.message("owned", "assistant", 3, `{"content":[{"type":"tool","state":{"status":"completed","metadata":{"output":"c"}}}]}`)
	p := New(f.root, nil)
	owner := model.SessionRef{Agent: model.AgentOpenCode, ID: "owned"}
	for _, test := range []struct {
		ref model.SessionRef
		key string
	}{
		{model.SessionRef{Agent: model.AgentCodex, ID: "owned"}, "v2:1:file:0"},
		{model.SessionRef{Agent: model.AgentOpenCode, ID: ""}, "v2:1:file:0"},
		{owner, "v2:2:file:0"}, // wrong session
		{owner, "v2:3:file:0"}, // wrong row type
		{owner, "v2:1:tool:0"}, // wrong key type
		{owner, "v2:1:file:1"}, // missing index
		{owner, "v2:1:file:-1"},
		{owner, "v2:1:file:99999999999999999999999999"},
		{owner, "v2:1:file:0:extra"},
		{owner, "v1:1:file:0"},
	} {
		if blob, err := p.Blob(t.Context(), test.ref, test.key); err == nil {
			t.Errorf("Blob(%+v, %q) = %q; want error", test.ref, test.key, blob)
		}
	}
	if _, err := p.Load(t.Context(), model.SessionRef{Agent: model.AgentCodex, ID: "owned"}); err == nil {
		t.Fatal("accepted other agent's ref")
	}
	if _, err := p.Load(t.Context(), model.SessionRef{Agent: model.AgentOpenCode, ID: "missing"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing Load error = %v", err)
	}
	// A file's encoded bytes may exceed the bound; never return a partial blob.
	big := strings.Repeat("A", (blobBudget*4/3)+16)
	f.message("owned", "user", 4, fmt.Sprintf(`{"files":[{"data":%q}]}`, big))
	if _, err := p.Blob(t.Context(), owner, "v2:4:file:0"); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Errorf("oversize Blob error = %v", err)
	}
}

func TestLoadV2OnlyFallback(t *testing.T) {
	p := New(t.TempDir(), nil)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "v1-id"}
	if _, err := p.Load(t.Context(), ref); !errors.Is(err, ErrUnsupportedLoad) {
		t.Errorf("no DB Load = %v", err)
	}
	f := newScanFixture(t)
	p = New(f.root, nil)
	if _, err := f.db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO session (id) VALUES ('v1-id')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load(t.Context(), ref); !errors.Is(err, ErrUnsupportedLoad) {
		t.Errorf("v1-only Load = %v", err)
	}
	if _, err := p.Blob(t.Context(), ref, "v1:id:file"); !errors.Is(err, ErrUnsupportedBlob) {
		t.Errorf("v1 Blob = %v", err)
	}
	if err := os.Remove(p.dbPath()); err != nil {
		t.Fatal(err)
	}
}
