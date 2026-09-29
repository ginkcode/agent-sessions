package handoff_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/model"
)

func TestCollectDescendants(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	rootMeta := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "root"},
		CreatedAt: now,
	}

	child1 := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "child-1"},
		ParentID:  "root",
		CreatedAt: now.Add(2 * time.Minute),
	}
	child2 := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "child-0"},
		ParentID:  "root",
		CreatedAt: now.Add(1 * time.Minute),
	}
	grandchild := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "grandchild"},
		ParentID:  "child-1",
		CreatedAt: now.Add(3 * time.Minute),
	}
	cycle := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "cycle-child"},
		ParentID:  "cycle-child", // self-parent or cycle
		CreatedAt: now.Add(4 * time.Minute),
	}
	otherAgentChild := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentCodex, ID: "codex-child"},
		ParentID:  "root",
		CreatedAt: now.Add(1 * time.Minute),
	}
	unrelated := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "other"},
		CreatedAt: now,
	}

	all := []model.SessionMeta{rootMeta, child1, child2, grandchild, cycle, otherAgentChild, unrelated}

	load := func(_ context.Context, ref model.SessionRef) (*model.Transcript, error) {
		return &model.Transcript{
			Meta: model.SessionMeta{Ref: ref},
		}, nil
	}

	ts, err := handoff.Collect(ctx, rootMeta, all, load)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(ts) != 4 {
		t.Fatalf("expected 4 transcripts (root + 3 descendants), got %d", len(ts))
	}
	// Root must be first
	if ts[0].Meta.Ref.ID != "root" {
		t.Errorf("expected root first, got %s", ts[0].Meta.Ref.ID)
	}
	// Then child2 (created +1m), child1 (created +2m), grandchild (created +3m)
	expectedOrder := []string{"root", "child-0", "child-1", "grandchild"}
	for i, exp := range expectedOrder {
		if ts[i].Meta.Ref.ID != exp {
			t.Errorf("at index %d: expected %s, got %s", i, exp, ts[i].Meta.Ref.ID)
		}
	}
}

func TestContextFilePathSafety(t *testing.T) {
	temp := t.TempDir()

	// Safe ID
	p, err := handoff.ContextFilePath(temp, "sess-123_abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(temp, "handoffs", "sess-123_abc-full.md")
	if p != expected {
		t.Errorf("got %s, want %s", p, expected)
	}

	// Unsafe characters and path traversal
	p2, err := handoff.ContextFilePath(temp, "../../../etc/passwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Dir(p2) != filepath.Join(temp, "handoffs") {
		t.Errorf("escaped handoff dir: %s", p2)
	}

	// Subagent slash in ID
	p3, err := handoff.ContextFilePath(temp, "parent/subagent_123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Dir(p3) != filepath.Join(temp, "handoffs") {
		t.Errorf("escaped handoff dir: %s", p3)
	}

	// Empty ID
	if _, err := handoff.ContextFilePath(temp, ""); err == nil {
		t.Error("expected error for empty ID")
	}

	// Empty data home
	if _, err := handoff.ContextFilePath("", "sess-1"); err == nil {
		t.Error("expected error for empty data home")
	}
}

func TestPruneContextFiles(t *testing.T) {
	temp := t.TempDir()
	handoffDir := handoff.HandoffDir(temp)
	if err := os.MkdirAll(handoffDir, 0o700); err != nil {
		t.Fatal(err)
	}

	oldTime := time.Now().Add(-35 * 24 * time.Hour)

	// An old context file that SHOULD be pruned
	oldContext := filepath.Join(handoffDir, "old-full.md")
	if err := os.WriteFile(oldContext, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(oldContext, oldTime, oldTime)

	// A recent context file that should NOT be pruned
	recentContext := filepath.Join(handoffDir, "recent-full.md")
	if err := os.WriteFile(recentContext, []byte("recent"), 0o600); err != nil {
		t.Fatal(err)
	}

	// An old unrelated file that should NOT be pruned
	unrelated := filepath.Join(handoffDir, "keepme.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(unrelated, oldTime, oldTime)

	// SaveContextFile triggers pruning
	_, err := handoff.SaveContextFile(temp, "new-session", "new content")
	if err != nil {
		t.Fatalf("SaveContextFile failed: %v", err)
	}

	if _, err := os.Stat(oldContext); !os.IsNotExist(err) {
		t.Errorf("old context file was not pruned: %v", err)
	}
	if _, err := os.Stat(recentContext); err != nil {
		t.Errorf("recent context file was pruned: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated file was pruned: %v", err)
	}
}

func TestLaunchPromptSwitch(t *testing.T) {
	short := "Brief prompt"
	text, pointer := handoff.LaunchPrompt(short, "/path/to/file.md")
	if pointer || text != short {
		t.Errorf("short prompt should not switch to pointer: %s, %v", text, pointer)
	}

	long := string(make([]byte, handoff.MaxPromptArgBytes+1))
	text, pointer = handoff.LaunchPrompt(long, "/path/to/file.md")
	if !pointer || text != "Read /path/to/file.md completely, then continue with the engineering task." {
		t.Errorf("long prompt with file should switch to pointer: %s, %v", text, pointer)
	}

	// Long prompt without file cannot switch
	text, pointer = handoff.LaunchPrompt(long, "")
	if pointer || text != long {
		t.Errorf("long prompt without file should not switch: %v", pointer)
	}
}
