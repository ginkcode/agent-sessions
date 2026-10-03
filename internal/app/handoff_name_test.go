package app

import (
	"context"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// TestSaveHandoffDefaultName pins the file name offered by the save dialog.
// The dialog is cancelled, so nothing is rendered or written.
func TestSaveHandoffDefaultName(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want string
	}{
		{name: "plain id", id: "sess-root", want: "sess-root-handoff.md"},
		{name: "claude subagent id", id: "parent-uuid/agent-a1b2c3", want: "parent-uuid_agent-a1b2c3-handoff.md"},
		{name: "windows unsafe characters", id: `a:b?c*d"e<f>g|h\i`, want: "a_b_c_d_e_f_g_h_i-handoff.md"},
		{name: "traversal", id: `..\..\evil`, want: "evil-handoff.md"},
		{name: "only unsafe characters", id: `\/:`, want: "handoff.md"},
		{name: "empty id", id: "", want: "handoff.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _, _ := setupHandoffTest(t)
			var got string
			calls := 0
			app.saveDialogOverride = func(_ context.Context, defaultName string) (string, error) {
				calls++
				got = defaultName
				return "", nil // user cancelled
			}
			path, err := app.SaveHandoff(HandoffRequest{
				Ref:    model.SessionRef{Agent: model.AgentClaude, ID: tc.id},
				Target: model.AgentCodex,
			})
			if err != nil {
				t.Fatalf("SaveHandoff: %v", err)
			}
			if path != "" {
				t.Fatalf("cancelled SaveHandoff path = %q, want empty", path)
			}
			if calls != 1 {
				t.Fatalf("save dialog calls = %d, want 1", calls)
			}
			if got != tc.want {
				t.Fatalf("default name = %q, want %q", got, tc.want)
			}
		})
	}
}
