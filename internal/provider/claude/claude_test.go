package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
)

func TestLiveDetection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("procfs fixture; see TestLiveDetectionWindows")
	}
	tmp := t.TempDir()
	procFS := filepath.Join(tmp, "proc")
	root := filepath.Join(tmp, "claude")
	sessionsDir := filepath.Join(root, "sessions")

	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Process 101: matching start time 123456
	// Format: pid (comm with parentheses) state ... (field 22 is starttime)
	p101Stat := "101 (test) (prog) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 123456 21 22 23"
	if err := os.MkdirAll(filepath.Join(procFS, "101"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procFS, "101", "stat"), []byte(p101Stat), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Process 102: mismatch start time (reused PID)
	p102Stat := "102 (other) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 999999 21 22 23"
	if err := os.MkdirAll(filepath.Join(procFS, "102"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procFS, "102", "stat"), []byte(p102Stat), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Process 103 does not exist in procFS (dead process)

	// Write session JSON files
	s1 := `{"pid":101,"sessionId":"sess-live","procStart":"123456","status":"working","updatedAt":1790514010000}`
	s2 := `{"pid":102,"sessionId":"sess-reused","procStart":"123456","status":"idle","updatedAt":1790514010000}`
	s3 := `{"pid":103,"sessionId":"sess-dead","procStart":"123456","status":"idle","updatedAt":1790514010000}`

	if err := os.WriteFile(filepath.Join(sessionsDir, "101.json"), []byte(s1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "102.json"), []byte(s2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "103.json"), []byte(s3), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ignore .key files
	if err := os.WriteFile(filepath.Join(sessionsDir, "101.key"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := claude.New(root, nil)
	p.SetProcFS(procFS)

	live, err := p.Live(context.Background())
	if err != nil {
		t.Fatalf("Live failed: %v", err)
	}

	if len(live) != 1 {
		t.Fatalf("expected 1 live session, got %d: %v", len(live), live)
	}
	info, ok := live["sess-live"]
	if !ok {
		t.Fatalf("sess-live not found in live map: %v", live)
	}
	if info.PID != 101 || info.Status != "working" {
		t.Fatalf("unexpected live info: %+v", info)
	}
}

func TestDiscover(t *testing.T) {
	// Set up fixture hierarchy under projects/
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	projDir := filepath.Join(root, "projects", "my-project")
	subDir := filepath.Join(projDir, "parent-uuid", "subagents")

	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Main session
	if err := os.WriteFile(filepath.Join(projDir, "main-sess.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Subagent session + meta
	if err := os.WriteFile(filepath.Join(subDir, "agent-a1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-a1.meta.json"), []byte(`{"agentType":"Explore"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	p := claude.New(root, nil)
	det, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if !det.Present {
		t.Fatalf("expected Present=true")
	}

	wp := p.WatchPaths()
	if len(wp) != 2 {
		t.Fatalf("expected 2 watch paths, got %d", len(wp))
	}

	cmd := p.ResumeCommand(model.SessionMeta{
		Ref: model.SessionRef{Agent: model.AgentClaude, ID: "main-sess"},
		CWD: "/home/dev/work",
	})
	if len(cmd.Argv) != 3 || cmd.Argv[2] != "main-sess" || cmd.Dir != "/home/dev/work" {
		t.Fatalf("unexpected resume command: %+v", cmd)
	}

	// Subagent resume command resumes parent
	subCmd := p.ResumeCommand(model.SessionMeta{
		Ref:      model.SessionRef{Agent: model.AgentClaude, ID: "parent-uuid/agent-a1"},
		ParentID: "parent-uuid",
		CWD:      "/home/dev/work",
	})
	if subCmd.Argv[2] != "parent-uuid" {
		t.Fatalf("expected subagent to resume parent UUID, got: %s", subCmd.Argv[2])
	}
}
