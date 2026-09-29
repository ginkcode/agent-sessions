package redact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/redact"
)

func TestTextCorpus(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		expectedRule  string
		expectedCount int
		shouldContain string
		shouldNotHave string
	}{
		{
			name:          "PEM private key",
			input:         "Here is the key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0Y123456789abcdef\n-----END RSA PRIVATE KEY-----\nDone.",
			expectedRule:  "pem",
			expectedCount: 1,
			shouldContain: "[REDACTED PRIVATE KEY]",
			shouldNotHave: "MIIEowIBAAKCAQEA0Y123456789abcdef",
		},
		{
			name:          "Anthropic key",
			input:         "Use sk-" + "ant-api03-abcdef1234567890_ABCDEF-123456789 for auth.",
			expectedRule:  "token",
			expectedCount: 1,
			shouldContain: "[REDACTED TOKEN]",
			shouldNotHave: "sk-" + "ant-api03-abcdef1234567890",
		},
		{
			name:          "OpenAI key",
			input:         "OPENAI_API_KEY=sk-" + "proj-1234567890abcdefghijklmnopqrstuvwxyz",
			expectedRule:  "token", // will match assignment and/or token
			expectedCount: 1,
			shouldNotHave: "sk-" + "proj-1234567890abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:          "GitHub PAT classic",
			input:         "git clone https://gh" + "p_1234567890abcdefghijklmnopqrstuvwxyz@github.com/org/repo",
			expectedRule:  "token",
			expectedCount: 1,
			shouldContain: "[REDACTED TOKEN]",
			shouldNotHave: "gh" + "p_1234567890abcdefghijklmnopqrstuvwxyz",
		},
		{
			name:          "GitHub PAT fine-grained",
			input:         "TOKEN=github_" + "pat_11AAAAAAA_1234567890abcdefghijklmnopqrstuvwxyz1234567890",
			expectedRule:  "token",
			expectedCount: 1,
			shouldNotHave: "github_" + "pat_11AAAAAAA_1234567890abcdef",
		},
		{
			name:          "Slack token",
			input:         "xo" + "xb-1234567890-1234567890123-abcdefghijklmnopqrstuvwx",
			expectedRule:  "token",
			expectedCount: 1,
			shouldContain: "[REDACTED TOKEN]",
			shouldNotHave: "xo" + "xb-1234567890-1234567890123",
		},
		{
			name:          "AWS Access Key",
			input:         "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			expectedRule:  "token",
			expectedCount: 1,
			shouldContain: "[REDACTED TOKEN]",
			shouldNotHave: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:          "JWT token",
			input:         "Authorization header: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			expectedRule:  "jwt",
			expectedCount: 1,
			shouldContain: "[REDACTED JWT]",
			shouldNotHave: "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		},
		{
			name:          "Bearer token",
			input:         "curl -H 'Authorization: Bearer mysecrettokenvalue1234567890abcdef' https://api.example.com",
			expectedRule:  "jwt",
			expectedCount: 1,
			shouldContain: "[REDACTED BEARER TOKEN]",
			shouldNotHave: "mysecrettokenvalue1234567890abcdef",
		},
		{
			name:          "Password assignment",
			input:         "database_password: super_secret_pass_1234\nother: safe",
			expectedRule:  "assignment",
			expectedCount: 1,
			shouldContain: "[REDACTED SECRET]",
			shouldNotHave: "super_secret_pass_1234",
		},
		{
			name:          "Linux home path",
			input:         "File located at /home/haith/Workspaces/project/main.go",
			expectedRule:  "home",
			expectedCount: 1,
			shouldContain: "~/Workspaces/project/main.go",
			shouldNotHave: "/home/haith",
		},
		{
			name:          "macOS home path",
			input:         "Error in /Users/developer/Library/Caches/agent-sessions",
			expectedRule:  "home",
			expectedCount: 1,
			shouldContain: "~/Library/Caches/agent-sessions",
			shouldNotHave: "/Users/developer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, counts := redact.Text(tc.input)
			if counts.Total() == 0 {
				t.Fatalf("expected redactions, got 0")
			}
			m := counts.Map()
			if m[tc.expectedRule] < tc.expectedCount {
				t.Errorf("expected rule %q >= %d, got %d (full counts: %+v)", tc.expectedRule, tc.expectedCount, m[tc.expectedRule], counts)
			}
			if tc.shouldContain != "" && !strings.Contains(out, tc.shouldContain) {
				t.Errorf("expected output to contain %q, got %q", tc.shouldContain, out)
			}
			if tc.shouldNotHave != "" && strings.Contains(out, tc.shouldNotHave) {
				t.Errorf("output still contains secret %q: %q", tc.shouldNotHave, out)
			}
		})
	}
}

func TestTranscriptRedaction(t *testing.T) {
	ts := &model.Transcript{
		Meta: model.SessionMeta{
			CWD:   "/home/user/work",
			Title: "Testing with sk-" + "ant-api03-12345678901234567890",
		},
		Messages: []model.Message{
			{
				Role: model.RoleUser,
				Parts: []model.Part{
					{
						Kind: model.PartText,
						Text: "Look at /home/user/secret.txt with password: supersecret123",
					},
					{
						Kind: model.PartTool,
						Tool: &model.ToolCall{
							Name:   "bash",
							Input:  json.RawMessage(`{"cmd":"curl -H 'Authorization: Bearer 123456789012345678901234567890' http://test"}`),
							Output: "error in /Users/admin/data: AKIAIOSFODNN7EXAMPLE invalid",
						},
					},
				},
			},
		},
	}

	counts := redact.Transcript(ts)
	if counts.Total() < 5 {
		t.Errorf("expected at least 5 redactions, got %d (%+v)", counts.Total(), counts)
	}

	if strings.Contains(ts.Meta.CWD, "/home/user") {
		t.Errorf("CWD not redacted: %s", ts.Meta.CWD)
	}
	if strings.Contains(ts.Meta.Title, "sk-ant") {
		t.Errorf("Title secret not redacted: %s", ts.Meta.Title)
	}
	if strings.Contains(ts.Messages[0].Parts[0].Text, "supersecret123") {
		t.Errorf("Message secret not redacted: %s", ts.Messages[0].Parts[0].Text)
	}
	if strings.Contains(string(ts.Messages[0].Parts[1].Tool.Input), "123456789012345678901234567890") {
		t.Errorf("Tool input secret not redacted: %s", string(ts.Messages[0].Parts[1].Tool.Input))
	}
	if strings.Contains(ts.Messages[0].Parts[1].Tool.Output, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("Tool output secret not redacted: %s", ts.Messages[0].Parts[1].Tool.Output)
	}
}
