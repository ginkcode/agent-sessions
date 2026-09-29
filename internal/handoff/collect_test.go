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

func TestLaunchPromptPointsAtFile(t *testing.T) {
	short := "Brief prompt"
	if got := handoff.LaunchPrompt(short, "/path/to/p-handoff.md"); got != "Read /path/to/p-handoff.md completely, then continue the task it describes." {
		t.Errorf("prompt with a file should point at it: %s", got)
	}

	// Without a prompt file the prompt itself is passed.
	if got := handoff.LaunchPrompt(short, ""); got != short {
		t.Errorf("prompt without a file should be inline: %s", got)
	}
}

func TestClearFiles(t *testing.T) {
	temp := t.TempDir()
	if _, err := handoff.SaveContextFile(temp, "s1", "full"); err != nil {
		t.Fatal(err)
	}
	if _, err := handoff.SavePromptFile(temp, "s1", "prompt"); err != nil {
		t.Fatal(err)
	}
	dir := handoff.HandoffDir(temp)
	stale := filepath.Join(dir, ".handoff-123.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if n, size := handoff.FilesUsage(temp); n != 3 || size != int64(len("full")+len("prompt")+1) {
		t.Errorf("usage = %d files, %d bytes", n, size)
	}
	removed, _, err := handoff.ClearFiles(temp)
	if err != nil || removed != 3 {
		t.Fatalf("ClearFiles = %d, %v", removed, err)
	}
	if n, _ := handoff.FilesUsage(temp); n != 0 {
		t.Errorf("files left after clear: %d", n)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated file was removed: %v", err)
	}

	// A missing directory is an empty cache, not an error.
	if removed, _, err := handoff.ClearFiles(filepath.Join(temp, "none")); err != nil || removed != 0 {
		t.Errorf("clear of missing dir = %d, %v", removed, err)
	}
}
