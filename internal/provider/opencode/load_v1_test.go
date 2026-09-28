package opencode

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/testutil/golden"
)

func TestLoadV1Golden(t *testing.T) {
	f := newScanFixture(t)
	f.v1Session("parent", map[string]any{"time_updated": int64(1_700_000_200_000)})
	f.v1Session("child", map[string]any{"parent_id": "parent", "time_updated": int64(1_700_000_200_000)})
	f.v1Session("other", map[string]any{"time_updated": int64(1_700_000_200_000)})
	f.v1Message("m-user", "parent", "user", 1_700_000_000_001)
	f.v1Part("p-text", "m-user", "parent", 1, `{"type":"text","text":"Please read"}`)
	f.v1Part("p-file", "m-user", "parent", 2, `{"type":"file","filename":"image.png","mime":"image/png","url":"data:image/png;base64,cGl4ZWxzAAE="}`)
	f.v1Message("m-synthetic", "parent", "user", 1_700_000_000_002)
	f.v1Part("p-synthetic", "m-synthetic", "parent", 1, `{"type":"text","text":"generated context","synthetic":true}`)
	f.v1Message("m-assistant", "parent", "assistant", 1_700_000_000_003)
	if _, err := f.db.Exec(`UPDATE message SET data=? WHERE id='m-assistant'`, `{"role":"assistant","time":{"created":1700000000003},"modelID":"large","providerID":"example","tokens":{"input":5,"output":6,"reasoning":2,"cache":{"read":3,"write":4}},"error":{"name":"test-secret","data":{"message":"secret"}}}`); err != nil {
		t.Fatal(err)
	}
	f.v1Part("p-reasoning", "m-assistant", "parent", 1, `{"type":"reasoning","text":"considering"}`)
	f.v1Part("p-tool", "m-assistant", "parent", 2, `{"type":"tool","tool":"bash","callID":"call-1","state":{"status":"completed","input":{"number":12345678901234567890},"output":"tool result"}}`)
	f.v1Part("p-tool-error", "m-assistant", "parent", 3, `{"type":"tool","tool":"read","callID":"call-2","state":{"status":"error","input":{"path":"missing"},"error":"not found"}}`)
	f.v1Part("p-task", "m-assistant", "parent", 4, `{"type":"tool","tool":"task","callID":"call-3","state":{"status":"completed","input":{},"output":"child result","metadata":{"parentSessionId":"parent","sessionId":"child"}}}`)
	f.v1Part("p-compaction", "m-assistant", "parent", 5, `{"type":"compaction","auto":true,"overflow":true}`)
	f.v1Part("p-agent", "m-assistant", "parent", 6, `{"type":"agent","name":"researcher"}`)
	f.v1Part("p-subtask", "m-assistant", "parent", 7, `{"type":"subtask","agent":"explore","description":"Find the implementation"}`)
	f.v1Part("p-retry", "m-assistant", "parent", 8, `{"type":"retry","attempt":2,"error":{"name":"APIError"}}`)
	f.v1Part("p-step", "m-assistant", "parent", 9, `{"type":"step-start"}`)

	p := New(f.root, nil)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "parent"}
	transcript, err := p.Load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Messages) != 3 {
		t.Fatalf("messages = %+v", transcript.Messages)
	}
	golden.JSON(t, "load_v1", transcript.Messages)
	raw, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"data:image", "cGl4ZWxzAAE=", "test-secret", `"message":"secret"`} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("transcript exposed %q", secret)
		}
	}
	var fileRef string
	for _, part := range transcript.Messages[0].Parts {
		if part.File != nil {
			fileRef = part.File.Ref
		}
	}
	blob, err := p.Blob(t.Context(), ref, fileRef)
	if err != nil || !bytes.Equal(blob, []byte("pixels\x00\x01")) {
		t.Fatalf("file blob = %q, %v", blob, err)
	}
	var task *model.ToolCall
	for _, part := range transcript.Messages[2].Parts {
		if part.Tool != nil && part.Tool.Name == "task" {
			task = part.Tool
		}
	}
	if task == nil || task.Child == nil || task.Child.ID != "child" {
		t.Fatalf("task child = %+v", task)
	}
}

func TestLoadPicksGenerationByUpdatedTime(t *testing.T) {
	f := newScanFixture(t)
	f.session("same", map[string]any{"time_updated": int64(100)})
	f.message("same", "user", 1, `{"text":"v2"}`)
	f.v1Session("same", map[string]any{"time_updated": int64(101)})
	f.v1Message("m1", "same", "user", 1)
	f.v1Part("p1", "m1", "same", 1, `{"type":"text","text":"v1"}`)
	p := New(f.root, nil)
	ref := model.SessionRef{Agent: model.AgentOpenCode, ID: "same"}
	transcript, err := p.Load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := transcript.Messages[0].Parts[0].Text; got != "v1" {
		t.Fatalf("newer generation text = %q", got)
	}
	if _, err := f.db.Exec(`UPDATE session_v2 SET time_updated=101 WHERE id='same'`); err != nil {
		t.Fatal(err)
	}
	transcript, err = p.Load(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := transcript.Messages[0].Parts[0].Text; got != "v2" {
		t.Fatalf("tie generation text = %q", got)
	}
}

func TestV1BlobRoundTripAndOwnership(t *testing.T) {
	f := newScanFixture(t)
	f.v1Session("owner", nil)
	f.v1Session("other", nil)
	f.v1Message("m1", "owner", "user", 1)
	f.v1Part("part:file", "m1", "owner", 1, `{"type":"file","url":"data:text/plain;base64,aGVsbG8="}`)
	f.v1Message("m2", "owner", "assistant", 2)
	long := strings.Repeat("x", outputBudget+1)
	tool, err := json.Marshal(map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": "completed", "output": long}})
	if err != nil {
		t.Fatal(err)
	}
	f.v1Part("part:tool", "m2", "owner", 2, string(tool))
	p := New(f.root, nil)
	owner := model.SessionRef{Agent: model.AgentOpenCode, ID: "owner"}
	blob, err := p.Blob(t.Context(), owner, "v1:part:file:file:0")
	if err != nil || string(blob) != "hello" {
		t.Fatalf("file blob = %q, %v", blob, err)
	}
	if _, err := p.Blob(t.Context(), model.SessionRef{Agent: model.AgentOpenCode, ID: "other"}, "v1:part:file:file:0"); err == nil {
		t.Fatal("accepted blob from another session")
	}
	transcript, err := p.Load(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	var outputRef string
	for _, message := range transcript.Messages {
		for _, part := range message.Parts {
			if part.Tool != nil {
				outputRef = part.Tool.OutputRef
			}
		}
	}
	if outputRef == "" {
		t.Fatal("large tool output did not produce blob ref")
	}
	blob, err = p.Blob(t.Context(), owner, outputRef)
	if err != nil || string(blob) != long {
		t.Fatalf("tool blob length = %d, %v", len(blob), err)
	}
}

func TestV1BlobRejectsPartWhoseMessageBelongsElsewhere(t *testing.T) {
	f := newScanFixture(t)
	f.v1Session("owner", nil)
	f.v1Session("other", nil)
	f.v1Message("m-other", "other", "assistant", 1)
	// The part claims owner, but its message belongs to other; loadV1 never
	// shows it for owner, so Blob must not serve it either.
	f.v1Part("p-stray", "m-other", "owner", 1, `{"type":"tool","tool":"bash","state":{"status":"completed","output":"hidden"}}`)
	p := New(f.root, nil)
	if _, err := p.Blob(t.Context(), model.SessionRef{Agent: model.AgentOpenCode, ID: "owner"}, "v1:p-stray:tool:0"); err == nil {
		t.Fatal("served a part whose message belongs to another session")
	}
}
