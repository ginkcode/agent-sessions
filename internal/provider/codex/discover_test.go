package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func TestDiscover_BasicTwoDayAndArchived(t *testing.T) {
	root := platform.TempDir(t)

	// Create directories for two days and archived
	day1 := filepath.Join(root, "sessions", "2026", "09", "27")
	day2 := filepath.Join(root, "sessions", "2026", "09", "28")
	archived := filepath.Join(root, "archived_sessions")

	for _, dir := range []string{day1, day2, archived} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Write rollout files
	f1 := filepath.Join(day1, "rollout-2026-09-27T10-00-00-11111111-1111-1111-1111-111111111111.jsonl")
	f2 := filepath.Join(day2, "rollout-2026-09-28T14-30-00-22222222-2222-2222-2222-222222222222.jsonl")
	f3 := filepath.Join(archived, "rollout-2026-09-20T08-15-00-33333333-3333-3333-3333-333333333333.jsonl")

	for _, f := range []string{f1, f2, f3} {
		if err := os.WriteFile(f, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Also write non-rollout files that must be ignored
	ignoredFiles := []string{
		filepath.Join(day1, "not-a-rollout.jsonl"),
		filepath.Join(day1, "rollout-test.txt"),
		filepath.Join(root, "sessions", "2026", "09", "rollout-wrong-depth.jsonl"),
		filepath.Join(root, "sessions", "2026", "rollout-wrong-depth.jsonl"),
		filepath.Join(root, "sessions", "rollout-wrong-depth.jsonl"),
		filepath.Join(root, "rollout-in-root.jsonl"),
		filepath.Join(archived, "other.json"),
		filepath.Join(root, "auth.json"),
	}
	for _, f := range ignoredFiles {
		if err := os.WriteFile(f, []byte("ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := New(root, nil)
	files, err := p.discover(context.Background())
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}

	normRoot := pathutil.NormalizeDir(root)
	wantF1 := filepath.Join(normRoot, "sessions", "2026", "09", "27", "rollout-2026-09-27T10-00-00-11111111-1111-1111-1111-111111111111.jsonl")
	wantF2 := filepath.Join(normRoot, "sessions", "2026", "09", "28", "rollout-2026-09-28T14-30-00-22222222-2222-2222-2222-222222222222.jsonl")
	wantF3 := filepath.Join(normRoot, "archived_sessions", "rollout-2026-09-20T08-15-00-33333333-3333-3333-3333-333333333333.jsonl")

	if len(files) != 3 {
		t.Fatalf("discover returned %d files, want 3: %v", len(files), files)
	}

	// files are sorted
	expected := []string{wantF3, wantF1, wantF2}
	// Note: wantF3 ("archived_sessions") sorts before wantF1 ("sessions")
	for i, want := range expected {
		if files[i] != want {
			t.Errorf("files[%d] = %q, want %q", i, files[i], want)
		}
	}
}

func TestDiscover_DuplicateUUIDInActiveAndArchived(t *testing.T) {
	root := platform.TempDir(t)
	day := filepath.Join(root, "sessions", "2026", "09", "27")
	archived := filepath.Join(root, "archived_sessions")

	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatal(err)
	}

	// Same rollout filename in active sessions and archived_sessions
	name := "rollout-2026-09-27T10-00-00-same-uuid.jsonl"
	fActive := filepath.Join(day, name)
	fArchived := filepath.Join(archived, name)

	if err := os.WriteFile(fActive, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fArchived, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New(root, nil)
	files, err := p.discover(context.Background())
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}

	// Discovery returns all rollout files (deduplication happens in Scan/M1-11)
	if len(files) != 2 {
		t.Fatalf("discover returned %d files, want 2: %v", len(files), files)
	}
}

func TestDiscover_SymlinkEscapeRejected(t *testing.T) {
	root := platform.TempDir(t)
	outside := platform.TempDir(t)

	// External rollout target
	outsideRollout := filepath.Join(outside, "secret-rollout.jsonl")
	if err := os.WriteFile(outsideRollout, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	day := filepath.Join(root, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create symlink pointing outside the root
	escapeSymlink := filepath.Join(day, "rollout-escape.jsonl")
	platform.Symlink(t, outsideRollout, escapeSymlink)

	// Also create a valid regular rollout file
	validRollout := filepath.Join(day, "rollout-valid.jsonl")
	if err := os.WriteFile(validRollout, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var diag provider.Diagnostics
	p := New(root, nil)
	files, err := p.discoverWithDiagnostics(context.Background(), &diag)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("discover returned %d files, want 1 (escape symlink must be rejected): %v", len(files), files)
	}

	normRoot := pathutil.NormalizeDir(root)
	wantValid := filepath.Join(normRoot, "sessions", "2026", "09", "28", "rollout-valid.jsonl")
	if files[0] != wantValid {
		t.Errorf("files[0] = %q, want %q", files[0], wantValid)
	}

	if len(diag.Warnings) == 0 {
		t.Errorf("expected warning for symlink escape, got none")
	}
}

func TestDiscover_UnreadableFileSkippedWithWarning(t *testing.T) {
	root := platform.TempDir(t)
	day := filepath.Join(root, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}

	// Valid rollout
	valid := filepath.Join(day, "rollout-valid.jsonl")
	if err := os.WriteFile(valid, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unreadable rollout (mode 0000)
	unreadable := filepath.Join(day, "rollout-unreadable.jsonl")
	if err := os.WriteFile(unreadable, []byte("{}\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	var diag provider.Diagnostics
	p := New(root, nil)
	files, err := p.discoverWithDiagnostics(context.Background(), &diag)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}

	// In root environments where root can read 0000, both might be read;
	// if non-root, unreadable is warned and skipped. Either way, discover should not error.
	if len(files) < 1 {
		t.Fatalf("expected at least 1 valid file discovered, got %d", len(files))
	}
}

func TestIndexDBPath_NumericOrdering(t *testing.T) {
	root := platform.TempDir(t)

	// Create state_2.sqlite and state_10.sqlite
	// Lexical: "state_2" > "state_10"
	// Numeric: 10 > 2 -> state_10 must be picked
	state2 := filepath.Join(root, "state_2.sqlite")
	state10 := filepath.Join(root, "state_10.sqlite")
	stateIgnored := filepath.Join(root, "state_invalid.sqlite")
	stateNegative := filepath.Join(root, "state_-5.sqlite")

	for _, f := range []string{state2, state10, stateIgnored, stateNegative} {
		if err := os.WriteFile(f, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := New(root, nil)
	best, err := p.indexDBPath()
	if err != nil {
		t.Fatalf("indexDBPath failed: %v", err)
	}

	normRoot := pathutil.NormalizeDir(root)
	wantPath := filepath.Join(normRoot, "state_10.sqlite")
	if best != wantPath {
		t.Errorf("indexDBPath = %q, want %q (numeric sort state_10 > state_2)", best, wantPath)
	}
}

func TestIndexDBPath_EmptyOrMissing(t *testing.T) {
	t.Run("missing root", func(t *testing.T) {
		p := New(filepath.Join(platform.TempDir(t), "nonexistent"), nil)
		best, err := p.indexDBPath()
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
		if best != "" {
			t.Errorf("expected empty path, got %q", best)
		}
	})

	t.Run("no state files in root", func(t *testing.T) {
		p := New(platform.TempDir(t), nil)
		best, err := p.indexDBPath()
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
		if best != "" {
			t.Errorf("expected empty path, got %q", best)
		}
	})
}
