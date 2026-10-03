package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/testutil/golden"
)

func loadFixture(t *testing.T, name string) *model.Transcript {
	t.Helper()
	root, _ := fixtureRoot(t, name)
	p := New(root, nil)
	tx, err := p.Load(context.Background(), model.SessionRef{Agent: model.AgentClaude, ID: name})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// Project-specific paths and modtimes are omitted from fixture goldens.
// Message contents, counts, token usage, and title remain in the snapshot.
func normalizeTranscript(tx *model.Transcript) *model.Transcript {
	copy := *tx
	copy.Meta.SourcePath = "<fixture>"
	copy.Meta.CWD = "<fixture-cwd>"
	copy.Meta.RepoRoot = ""
	copy.Meta.CWDMissing = false
	return &copy
}

func TestLoadGolden(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"basic", "split_assistant", "compaction", "meta"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tx := loadFixture(t, name)
			golden.JSON(t, "load_"+name, normalizeTranscript(tx))
		})
	}
}

func TestLoadSplitAssistant(t *testing.T) {
	t.Parallel()
	tx := loadFixture(t, "split_assistant")
	var assistants []*model.Message
	for i := range tx.Messages {
		if tx.Messages[i].Role == model.RoleAssistant {
			assistants = append(assistants, &tx.Messages[i])
		}
	}
	if len(assistants) != 1 {
		t.Fatalf("assistant messages = %d, want 1", len(assistants))
	}
	msg := assistants[0]
	if len(msg.Parts) != 4 {
		t.Fatalf("assistant parts = %d, want 4", len(msg.Parts))
	}
	for i, want := range []model.PartKind{model.PartReasoning, model.PartText, model.PartTool, model.PartTool} {
		if msg.Parts[i].Kind != want {
			t.Errorf("part %d kind = %s, want %s", i, msg.Parts[i].Kind, want)
		}
	}
	if msg.Tokens.Output != 30 || msg.Tokens.Input != 50 {
		t.Errorf("last usage was not retained: %+v", msg.Tokens)
	}
	if got := msg.Parts[2].Tool; got == nil || got.Output != "func hello() {}" || got.Status != model.ToolCompleted {
		t.Errorf("Read result = %+v", got)
	}
	if got := msg.Parts[3].Tool; got == nil || got.Output != "main.go:1" || got.Status != model.ToolCompleted {
		t.Errorf("Grep result = %+v", got)
	}
	for _, m := range tx.Messages {
		if m.Role == model.RoleUser && len(m.Parts) == 0 {
			t.Errorf("tool-only user message was rendered: %+v", m)
		}
	}
}

func TestLoadCompactionAndMeta(t *testing.T) {
	t.Parallel()
	tx := loadFixture(t, "compaction")
	if len(tx.Messages) != 5 {
		t.Fatalf("compaction messages = %d, want 5", len(tx.Messages))
	}
	comp := tx.Messages[2]
	if comp.Role != model.RoleSystem || len(comp.Parts) != 1 || comp.Parts[0].Kind != model.PartCompaction {
		t.Fatalf("compaction message = %+v", comp)
	}
	want := "Context compacted (auto · 169,569 → 14,495 tokens)\n\nThe previous conversation focused on feature implementation."
	if comp.Parts[0].Text != want {
		t.Errorf("compaction text = %q, want %q", comp.Parts[0].Text, want)
	}

	tx = loadFixture(t, "meta")
	if len(tx.Messages) != 6 {
		t.Fatalf("meta messages = %d, want 6", len(tx.Messages))
	}
	for i := 0; i < 4; i++ {
		if !tx.Messages[i].IsMeta {
			t.Errorf("message %d should be meta: %+v", i, tx.Messages[i])
		}
	}
	if tx.Messages[3].Role != model.RoleSystem || tx.Messages[3].Parts[0].Kind != model.PartNotice {
		t.Errorf("attachment not a system notice: %+v", tx.Messages[3])
	}
	if tx.Messages[4].IsMeta {
		t.Errorf("conversational user record marked meta: %+v", tx.Messages[4])
	}
}

func TestLoadPartialAndUnknown(t *testing.T) {
	t.Parallel()
	tx := loadFixture(t, "partial_last_line")
	if len(tx.Messages) != 2 {
		t.Errorf("partial JSON line should not create a message; got %d", len(tx.Messages))
	}
	root, path := fixtureRoot(t, "unknown_types")
	p := New(root, nil)
	src := source{Path: path}
	tx, diag, err := p.loadFile(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Messages) != 2 {
		t.Errorf("unknown records should not render; got %d messages", len(tx.Messages))
	}
	if diag.UnknownTypes["future-widget"] != 2 || diag.UnknownTypes["unknown-action"] != 1 {
		t.Errorf("unknown record counts = %+v", diag.UnknownTypes)
	}
}

func TestLoadOutputBlob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "p")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("a", maxOutputBytes-1) + "é" + "FIN"
	contents := fmt.Sprintf(""+
		"{\"type\":\"assistant\",\"message\":{\"id\":\"m\",\"content\":[{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Bash\",\"input\":{}}]}}\n"+
		"{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"tool-1\",\"content\":%q}]}}\n", payload)
	path := filepath.Join(project, "s.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s"}
	tx, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tool := tx.Messages[0].Parts[0].Tool
	if tool.Status != model.ToolCompleted || !tool.OutputTruncated {
		t.Errorf("tool status/truncated = %+v", tool)
	}
	if len(tool.Output) > maxOutputBytes || !strings.HasSuffix(tool.Output, "a") {
		t.Errorf("output truncated at wrong boundary: len=%d", len(tool.Output))
	}
	if !strings.HasPrefix(tool.OutputRef, "rec:") {
		t.Fatalf("output ref = %q", tool.OutputRef)
	}
	got, err := p.Blob(context.Background(), ref, tool.OutputRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("Blob output = %d bytes, want %d", len(got), len(payload))
	}
}

func TestLoadImageBlob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "p")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	image := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	line := fmt.Sprintf(`{"type":"user","timestamp":"2026-09-20T01:02:03Z","message":{"content":[{"type":"text","text":"What is this?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}}`, base64.StdEncoding.EncodeToString(image))
	path := filepath.Join(project, "s.jsonl")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s"}
	tx, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Messages) != 1 || len(tx.Messages[0].Parts) != 2 {
		t.Fatalf("image messages = %+v", tx.Messages)
	}
	file := tx.Messages[0].Parts[1].File
	if file == nil || file.Mime != "image/png" || file.Ref != "rec:0:1" {
		t.Fatalf("image file ref = %+v", file)
	}
	got, err := p.Blob(context.Background(), ref, file.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, image) {
		t.Errorf("Blob image = %v, want %v", got, image)
	}
}

func TestBlobFileAndTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "p")
	sessionDir := filepath.Join(project, "s", "tool-results")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	toolResult := []byte("full output")
	if err := os.WriteFile(filepath.Join(sessionDir, "t.txt"), toolResult, 0o600); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"Bash"}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"Saved to /tmp/project/tool-results/t.txt"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, "s.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s"}
	tx, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	tool := tx.Messages[0].Parts[0].Tool
	if tool.OutputRef != "file:t.txt" || !tool.OutputTruncated {
		t.Fatalf("file-backed output = %+v", tool)
	}
	got, err := p.Blob(context.Background(), ref, tool.OutputRef)
	if err != nil || !bytes.Equal(got, toolResult) {
		t.Fatalf("file Blob = %q, %v", got, err)
	}
	for _, key := range []string{"file:../secret", "file:/etc/passwd", "file:..", "file:", "rec:-1:0", "bogus"} {
		if _, err := p.Blob(context.Background(), ref, key); err == nil {
			t.Errorf("Blob accepted invalid key %q", key)
		}
	}
}

func TestLoadToolErrorAndPending(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "p")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	contents := `{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"good","name":"Bash"},{"type":"tool_use","id":"bad","name":"Bash"},{"type":"tool_use","id":"pending","name":"Bash"}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"good","content":"ok"},{"type":"tool_result","tool_use_id":"bad","content":"failure","is_error":true}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, "s.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	tx, err := p.Load(context.Background(), model.SessionRef{Agent: model.AgentClaude, ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(tx.Messages))
	}
	for i, want := range []model.ToolStatus{model.ToolCompleted, model.ToolError, model.ToolUnknown} {
		if tx.Messages[0].Parts[i].Tool.Status != want {
			t.Errorf("tool %d status = %s, want %s", i, tx.Messages[0].Parts[i].Tool.Status, want)
		}
	}
}

func TestLoadSubagent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "project-x")
	parentID := "11111111-2222-3333-4444-555555555555"
	subDir := filepath.Join(project, parentID, "subagents")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{parentID + ".jsonl", parentID + "/subagents/agent-a1.jsonl", parentID + "/subagents/agent-a1.meta.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", "project-x", name))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(project, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := New(root, nil)
	parentRef := model.SessionRef{Agent: model.AgentClaude, ID: parentID}
	parent, err := p.Load(context.Background(), parentRef)
	if err != nil {
		t.Fatal(err)
	}
	tool := parent.Messages[1].Parts[0].Tool
	childID := parentID + "/agent-a1"
	if tool.Child == nil || tool.Child.ID != childID {
		t.Fatalf("parent tool child = %+v, want %s", tool.Child, childID)
	}
	child, err := p.Load(context.Background(), *tool.Child)
	if err != nil {
		t.Fatal(err)
	}
	if child.Meta.ParentID != parentID || child.Meta.ParentToolCallID != "parent-call-1" || child.Meta.AgentName != "Explore" {
		t.Errorf("child meta = %+v", child.Meta)
	}
	if child.Meta.Title != "Search for the target function" {
		t.Errorf("child title = %q", child.Meta.Title)
	}
	if len(child.Messages) != 2 || !child.Messages[0].IsSidechain || !child.Messages[1].IsSidechain {
		t.Errorf("child sidechain messages = %+v", child.Messages)
	}
}

func TestLoadCanceled(t *testing.T) {
	t.Parallel()
	root, _ := fixtureRoot(t, "basic")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(root, nil).Load(ctx, model.SessionRef{Agent: model.AgentClaude, ID: "basic"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Load canceled error = %v", err)
	}
}

func TestClipOutputToolResultsRef(t *testing.T) {
	tests := []struct{ out, ref string }{
		{"Saved to /tmp/project/tool-results/t.txt", "file:t.txt"},
		{`Saved to C:\Users\dev\project\tool-results\t.txt.`, "file:t.txt."},
		{`Saved to "C:\p\tool-results\w.txt" then /x/tool-results/u.txt`, "file:u.txt"},
		{`Saved to /x/tool-results/u.txt then C:\p\tool-results\w.txt`, "file:w.txt"},
		{`Saved to C:\p\tool-results\..\x`, ""},
		{"no reference", ""},
	}
	for _, tt := range tests {
		if _, ref, _ := clipOutput(tt.out, 0, "t"); ref != tt.ref {
			t.Errorf("clipOutput(%q) ref = %q, want %q", tt.out, ref, tt.ref)
		}
	}
}
