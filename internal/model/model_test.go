package model_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

func TestTokenUsage(t *testing.T) {
	u1 := model.TokenUsage{
		Input:      10,
		Output:     20,
		Reasoning:  5,
		CacheRead:  30,
		CacheWrite: 40,
	}
	u2 := model.TokenUsage{
		Input:      1,
		Output:     2,
		Reasoning:  3,
		CacheRead:  4,
		CacheWrite: 5,
	}
	sum := u1.Add(u2)
	expected := model.TokenUsage{
		Input:      11,
		Output:     22,
		Reasoning:  8,
		CacheRead:  34,
		CacheWrite: 45,
	}
	if sum != expected {
		t.Fatalf("expected %+v, got %+v", expected, sum)
	}
}

func TestMessageCountsTotal(t *testing.T) {
	c := model.MessageCounts{
		User:      12,
		Assistant: 34,
		ToolCalls: 100,
	}
	if c.Total() != 46 {
		t.Fatalf("expected total 46, got %d", c.Total())
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"xin chào thế giới", 8, "xin chào"},
		{"🚀🌟🔥✨🎉", 3, "🚀🌟🔥"},
	}
	for _, tc := range tests {
		got := model.TruncateRunes(tc.input, tc.n)
		if got != tc.want {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}

func TestOneLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "hello world"},
		{"  hello   \n\t  world  \r\n", "hello world"},
		{"\n\n\n", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		got := model.OneLine(tc.input)
		if got != tc.want {
			t.Errorf("OneLine(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTranscriptJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	childRef := model.SessionRef{
		Agent: model.AgentClaude,
		ID:    "sub-1",
	}

	tr := model.Transcript{
		Meta: model.SessionMeta{
			Ref: model.SessionRef{
				Agent: model.AgentClaude,
				ID:    "sess-123",
			},
			ParentID:         "",
			ParentToolCallID: "",
			SourcePath:       "/home/dev/.claude/projects/test/sess-123.jsonl",
			CWD:              "/home/dev/workspaces/app",
			RepoRoot:         "/home/dev/workspaces/app",
			CWDMissing:       false,
			GitBranch:        "feature/ui",
			Title:            "Fix login issue",
			FirstPrompt:      "Please fix login issue",
			Model:            "claude-sonnet-5",
			AgentName:        "claude-code",
			CreatedAt:        now,
			UpdatedAt:        now.Add(10 * time.Minute),
			Counts: model.MessageCounts{
				User:      2,
				Assistant: 2,
				ToolCalls: 1,
			},
			Tokens: model.TokenUsage{
				Input:      1000,
				Output:     200,
				Reasoning:  50,
				CacheRead:  500,
				CacheWrite: 100,
			},
			CostUSD:      0.015,
			Archived:     false,
			Live:         true,
			LiveStatus:   "idle",
			AgentVersion: "2.1.280",
		},
		Messages: []model.Message{
			{
				ID:    "msg-1",
				Role:  model.RoleUser,
				Time:  now,
				Model: "",
				Parts: []model.Part{
					{
						Kind: model.PartText,
						Text: "Please fix login issue",
					},
				},
				IsMeta:      false,
				IsSidechain: false,
				Tokens:      model.TokenUsage{},
			},
			{
				ID:    "msg-2",
				Role:  model.RoleAssistant,
				Time:  now.Add(5 * time.Second),
				Model: "claude-sonnet-5",
				Parts: []model.Part{
					{
						Kind: model.PartReasoning,
						Text: "Let me check the logs",
					},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							ID:              "call-1",
							Name:            "bash",
							Input:           json.RawMessage(`{"command":"cat login.log"}`),
							Output:          "error 401 unauthorized",
							OutputTruncated: false,
							OutputRef:       "",
							Status:          model.ToolCompleted,
							Child:           &childRef,
						},
					},
					{
						Kind: model.PartText,
						Text: "Found an auth error.",
					},
				},
				IsMeta:      false,
				IsSidechain: false,
				Tokens: model.TokenUsage{
					Input:  1000,
					Output: 200,
				},
			},
		},
	}

	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var tr2 model.Transcript
	if err := json.Unmarshal(data, &tr2); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if !reflect.DeepEqual(tr, tr2) {
		t.Fatalf("round-trip mismatch:\nwant: %+v\ngot:  %+v", tr, tr2)
	}
}
