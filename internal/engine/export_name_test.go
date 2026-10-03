package engine

import (
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
)

func TestExportFileName(t *testing.T) {
	cases := []struct {
		name string
		meta model.SessionMeta
		want string
	}{
		{
			name: "uuid keeps its first block",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentCodex, ID: "019a3b2c-7d4e-7f00-8a1b-2c3d4e5f6a7b"}, CWD: "/home/me/work/api-server/"},
			want: "codex_api-server_019a3b2c.agent-session.zip",
		},
		{
			name: "windows path and unsafe characters",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "a:b?c"}, CWD: `C:\Users\me\My App?`},
			want: "claude-code_My-App_a-b-c.agent-session.zip",
		},
		{
			name: "windows long path prefix",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentCodex, ID: "019a3b2c-7d4e-7f00-8a1b-2c3d4e5f6a7b"}, CWD: `\\?\D:\Workspaces\x`},
			want: "codex_x_019a3b2c.agent-session.zip",
		},
		{
			name: "windows unc path with trailing separator",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "5e6f7a8b-0000-4000-8000-000000000000"}, CWD: `\\fileserver\share\team-repo\`},
			want: "claude-code_team-repo_5e6f7a8b.agent-session.zip",
		},
		{
			name: "windows mixed separators",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentCodex, ID: "019a3b2c-7d4e"}, CWD: `C:/Users/me\repos/web-app`},
			want: "codex_web-app_019a3b2c.agent-session.zip",
		},
		{
			// The drive root has no directory name; the drive letter is kept
			// once its ":" is sanitized and trimmed.
			name: "windows drive root",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentOpenCode, ID: "ses_1"}, CWD: `C:\`},
			want: "opencode_C_ses_1.agent-session.zip",
		},
		{
			name: "windows repeated trailing backslashes",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentCodex, ID: "019a3b2c-7d4e"}, CWD: `D:\work\api\\\`},
			want: "codex_api_019a3b2c.agent-session.zip",
		},
		{
			name: "claude subagent id keeps the parent block",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "7f3c9a12-1111-4222-8333-444455556666/agent-a1b2c3d4"}, CWD: `C:\Users\me\proj`},
			want: "claude-code_proj_7f3c9a12.agent-session.zip",
		},
		{
			name: "claude subagent id cut at the slash",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "parent/agent-a1b2c3d4"}, CWD: `C:\Users\me\proj`},
			want: "claude-code_proj_parent.agent-session.zip",
		},
		{
			name: "id without blocks is capped",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentOpenCode, ID: "ses_f2d527962ffes3ZSYVg7xky34Z"}, CWD: "/srv/app"},
			want: "opencode_app_ses_f2d52796.agent-session.zip",
		},
		{
			name: "no directory",
			meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentOpenCode, ID: "ses_1"}, CWD: "/"},
			want: "opencode_ses_1.agent-session.zip",
		},
		{
			name: "nothing known",
			meta: model.SessionMeta{},
			want: "session.agent-session.zip",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExportFileName(tc.meta); got != tc.want {
				t.Fatalf("ExportFileName = %q, want %q", got, tc.want)
			}
		})
	}
}
