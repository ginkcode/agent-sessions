package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/watch"
)

// waitFor polls cond until it returns true or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWatcherCoalescesRapidWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	projDir := filepath.Join(root, "projects", "-home-user-project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sessionFile := filepath.Join(projDir, "session-abc.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.Watch = []string{root}

	var mu sync.Mutex
	var changes []watch.Change
	onChange := func(c watch.Change) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, c)
	}

	w, err := watch.Start(ctx, provider.Set{fake}, onChange)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w()

	// Write to the session file multiple times rapidly.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(sessionFile, []byte(`{"line":1}`), 0o600); err != nil {
			t.Fatalf("WriteFile %d: %v", i, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(changes) == 0 {
		t.Fatal("expected at least one coalesced change notification")
	}
	if changes[0].Agent != model.AgentClaude {
		t.Fatalf("change agent = %q, want %q", changes[0].Agent, model.AgentClaude)
	}
}

func TestWatcherDetectsNewNestedDirectories(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.Watch = []string{root}

	var mu sync.Mutex
	var changes []watch.Change
	onChange := func(c watch.Change) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, c)
	}

	w, err := watch.Start(ctx, provider.Set{fake}, onChange)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w()

	// Give the watcher time to register the root before creating children.
	time.Sleep(200 * time.Millisecond)

	// Create a nested directory and a JSONL file inside it.
	newDir := filepath.Join(root, "projects", "-home-user-newproj")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sessionFile := filepath.Join(newDir, "session-new.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(changes) == 0 {
		t.Fatal("expected change notification for nested directory creation")
	}
}

func TestWatcherCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.Watch = []string{root}

	w, err := watch.Start(ctx, provider.Set{fake}, func(watch.Change) {})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- w()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return within 3 seconds")
	}
}

func TestWatcherOpenCodeDBAndWAL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dbFile := filepath.Join(root, "opencode.db")
	walFile := filepath.Join(root, "opencode.db-wal")
	if err := os.WriteFile(dbFile, []byte("sqlite"), 0o600); err != nil {
		t.Fatalf("WriteFile db: %v", err)
	}
	if err := os.WriteFile(walFile, []byte("wal"), 0o600); err != nil {
		t.Fatalf("WriteFile wal: %v", err)
	}

	fake := providertest.NewFake(model.AgentOpenCode, "OpenCode")
	fake.Watch = []string{dbFile, walFile}

	var mu sync.Mutex
	var changes []watch.Change
	onChange := func(c watch.Change) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, c)
	}

	w, err := watch.Start(ctx, provider.Set{fake}, onChange)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w()

	time.Sleep(200 * time.Millisecond)

	// A WAL write (checkpoint activity) must notify; the watcher observes the
	// parent directory rather than the replaceable db inode.
	if err := os.WriteFile(walFile, []byte("wal-more"), 0o600); err != nil {
		t.Fatalf("WriteFile wal: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(changes) == 0 {
		t.Fatal("expected change notification for WAL write")
	}
	if changes[0].Agent != model.AgentOpenCode {
		t.Fatalf("change agent = %q, want %q", changes[0].Agent, model.AgentOpenCode)
	}
}

func TestWatcherCodexRolloutFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fake := providertest.NewFake(model.AgentCodex, "Codex")
	fake.Watch = []string{sessionsDir}

	var mu sync.Mutex
	var changes []watch.Change
	onChange := func(c watch.Change) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, c)
	}

	w, err := watch.Start(ctx, provider.Set{fake}, onChange)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w()

	time.Sleep(200 * time.Millisecond)

	// An unrelated file must not notify: 600 ms exceeds the 300 ms debounce
	// plus dispatch, so silence here means the event was filtered out.
	if err := os.WriteFile(filepath.Join(sessionsDir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile notes: %v", err)
	}
	time.Sleep(600 * time.Millisecond)

	mu.Lock()
	if len(changes) != 0 {
		mu.Unlock()
		t.Fatalf("expected no change for unrelated file, got %d", len(changes))
	}
	mu.Unlock()

	// A rollout under a newly created nested day directory must notify.
	rolloutDir := filepath.Join(sessionsDir, "2026", "09", "29")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("MkdirAll rollout dir: %v", err)
	}
	rolloutFile := filepath.Join(rolloutDir, "rollout-uuid123.jsonl")
	if err := os.WriteFile(rolloutFile, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile rollout: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if len(changes) == 0 {
		t.Fatal("expected change notification for rollout creation")
	}
	if changes[0].Agent != model.AgentCodex {
		t.Fatalf("change agent = %q, want %q", changes[0].Agent, model.AgentCodex)
	}
}
