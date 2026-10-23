package opencode

import (
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestLastMessageTurn(t *testing.T) {
	tests := []struct {
		kind, finish    string
		failed, running bool
		want            string
	}{
		{"", "", false, false, ""},
		{"user", "", false, false, model.TurnWorking},
		{"idle", "", false, false, model.TurnDone},
		{"assistant", "", false, false, model.TurnWorking},
		{"assistant", "tool-calls", false, false, model.TurnWorking},
		{"assistant", "unknown", false, false, model.TurnWorking},
		{"assistant", "stop", false, false, model.TurnDone},
		{"assistant", "length", false, false, model.TurnDone},
		{"assistant", "", true, false, model.TurnDone},
		{"assistant", "stop", true, true, model.TurnTool},
	}
	for _, tt := range tests {
		if got := lastMessageTurn(tt.kind, tt.finish, tt.failed, tt.running); got != tt.want {
			t.Errorf("lastMessageTurn(%q, %q, %t, %t) = %q, want %q", tt.kind, tt.finish, tt.failed, tt.running, got, tt.want)
		}
	}
}

func TestScanV2Turns(t *testing.T) {
	f := newScanFixture(t)
	tool := func(status string) string {
		return `{"content":[{"type":"text","text":"x"},{"type":"tool","id":"c1","name":"bash","state":{"status":"` + status + `"}}]}`
	}
	turns := map[string]string{}
	add := func(id, want string, messages ...[2]string) {
		f.session(id, nil)
		for i, m := range messages {
			f.message(id, m[0], i+1, m[1])
		}
		turns[id] = want
	}
	add("asking", model.TurnWorking, [2]string{"user", `{"text":"hi"}`})
	add("running", model.TurnTool, [2]string{"user", `{"text":"hi"}`}, [2]string{"assistant", tool("running")})
	add("pending", model.TurnTool, [2]string{"user", `{"text":"hi"}`}, [2]string{"assistant", tool("pending")})
	add("ran", model.TurnWorking, [2]string{"user", `{"text":"hi"}`}, [2]string{"assistant", tool("completed")})
	add("finished", model.TurnDone, [2]string{"user", `{"text":"hi"}`}, [2]string{"assistant", `{"finish":"stop","content":[]}`})
	add("failed", model.TurnDone, [2]string{"user", `{"text":"hi"}`}, [2]string{"assistant", `{"error":{"name":"APIError"}}`})
	add("idle", model.TurnDone, [2]string{"user", `{"text":"hi"}`}, [2]string{"assistant", tool("completed")}, [2]string{"idle", `{}`}, [2]string{"system", `{}`})
	// The row type is known even when its data is not.
	add("invalid", model.TurnWorking, [2]string{"user", `{`})
	add("empty", "")
	for _, meta := range f.metas(t) {
		if want := turns[meta.Ref.ID]; meta.Turn != want {
			t.Errorf("%s: Turn = %q, want %q", meta.Ref.ID, meta.Turn, want)
		}
	}
}

func TestScanV1Turns(t *testing.T) {
	f := newScanFixture(t)
	if _, err := f.db.Exec(`DROP TABLE session_v2; DROP TABLE session_message`); err != nil {
		t.Fatal(err)
	}
	f.v1Session("v1", nil)
	f.v1Message("m-user", "v1", "user", 1_700_000_000_001)
	f.v1Part("p-text", "m-user", "v1", 1_700_000_000_002, `{"type":"text","text":"go"}`)
	f.v1Message("m-step", "v1", "assistant", 1_700_000_000_003)
	f.v1Part("p-tool", "m-step", "v1", 1_700_000_000_004, `{"type":"tool","tool":"bash","callID":"c1","state":{"status":"running","input":{}}}`)
	scan := func() model.SessionMeta {
		t.Helper()
		result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
		if err != nil || len(result.Changed) != 1 {
			t.Fatalf("scan = %+v, %v", result.Changed, err)
		}
		return result.Changed[0]
	}
	if got := scan().Turn; got != model.TurnTool {
		t.Fatalf("running tool: Turn = %q", got)
	}
	if _, err := f.db.Exec(`UPDATE part SET data=json_set(data,'$.state.status','completed') WHERE id='p-tool'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE message SET data=json_set(data,'$.finish','tool-calls') WHERE id='m-step'`); err != nil {
		t.Fatal(err)
	}
	if got := scan().Turn; got != model.TurnWorking {
		t.Fatalf("after tool calls: Turn = %q", got)
	}
	f.v1Message("m-answer", "v1", "assistant", 1_700_000_000_005)
	if _, err := f.db.Exec(`UPDATE message SET data=json_set(data,'$.finish','stop') WHERE id='m-answer'`); err != nil {
		t.Fatal(err)
	}
	if got := scan().Turn; got != model.TurnDone {
		t.Fatalf("answered: Turn = %q", got)
	}
}
