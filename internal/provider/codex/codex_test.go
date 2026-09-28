package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestProviderBasics(t *testing.T) {
	p := New("/tmp/fake-codex", nil)
	if p.ID() != model.AgentCodex {
		t.Fatalf("ID() = %q, want %q", p.ID(), model.AgentCodex)
	}
	if p.DisplayName() != "Codex" {
		t.Fatalf("DisplayName() = %q, want %q", p.DisplayName(), "Codex")
	}

	// ResumeCommand format: codex resume <id> in cwd
	meta := model.SessionMeta{
		Ref: model.SessionRef{Agent: model.AgentCodex, ID: "sess-1234"},
		CWD: "/home/user/project",
	}
	cmd := p.ResumeCommand(meta)
	if cmd.Dir != "/home/user/project" {
		t.Errorf("ResumeCommand.Dir = %q, want %q", cmd.Dir, "/home/user/project")
	}
	wantArgv := []string{"codex", "resume", "sess-1234"}
	if len(cmd.Argv) != len(wantArgv) {
		t.Fatalf("ResumeCommand.Argv len = %d, want %d", len(cmd.Argv), len(wantArgv))
	}
	for i := range wantArgv {
		if cmd.Argv[i] != wantArgv[i] {
			t.Errorf("ResumeCommand.Argv[%d] = %q, want %q", i, cmd.Argv[i], wantArgv[i])
		}
	}

	// Missing roots have no rollouts. Load/Blob return descriptive not-found errors.
	ctx := context.Background()
	if scan, err := p.Scan(ctx, provider.ScanState{}); err != nil || len(scan.Changed) != 0 || len(scan.Removed) != 0 {
		t.Errorf("Scan() = %+v, %v, want empty result", scan, err)
	}
	if _, err := p.Load(ctx, meta.Ref); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Load() err = %v, want not-found error for missing rollout", err)
	}
	if _, err := p.Blob(ctx, meta.Ref, "key"); err == nil || !strings.Contains(err.Error(), "unsupported key") {
		t.Errorf("Blob() err = %v, want unsupported-key error", err)
	}
}

func TestDetect_MissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nonexistent-codex-root")
	p := New(missing, nil)

	det, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect on missing root returned error: %v", err)
	}
	if det.Present {
		t.Errorf("Detect on missing root Present = true, want false")
	}
	if len(det.Roots) != 1 || det.Roots[0] != missing {
		t.Errorf("Detect.Roots = %v, want [%s]", det.Roots, missing)
	}
}

func TestDetect_FileNotDir(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("plain file"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New(filePath, nil)
	det, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect on plain file root returned error: %v", err)
	}
	if det.Present {
		t.Errorf("Detect on plain file root Present = true, want false")
	}
}

func TestDetect_EmptyRoot(t *testing.T) {
	empty := t.TempDir()
	p := New(empty, nil)

	det, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect on empty root returned error: %v", err)
	}
	if det.Present {
		t.Errorf("Detect on empty root Present = true, want false")
	}
}

func TestDetect_WithRollouts(t *testing.T) {
	root := t.TempDir()
	dayDir := filepath.Join(root, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dayDir, "rollout-2026-09-28T12-00-00-uuid1.jsonl")
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New(root, nil)
	det, err := p.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect with rollouts returned error: %v", err)
	}
	if !det.Present {
		t.Errorf("Detect with rollouts Present = false, want true")
	}
}

func TestDetect_ThreadsIndex(t *testing.T) {
	t.Run("empty threads table", func(t *testing.T) {
		root := t.TempDir()
		dbPath := filepath.Join(root, "state_5.sqlite")
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT);"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		_ = db.Close()

		p := New(root, nil)
		det, err := p.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect error: %v", err)
		}
		if det.Present {
			t.Errorf("Detect Present = true, want false (empty index alone is not a session)")
		}
		if len(det.Generations) != 1 || det.Generations[0] != "state:state_5.sqlite" {
			t.Errorf("Detect.Generations = %v, want [state:state_5.sqlite]", det.Generations)
		}
	})

	t.Run("populated threads table", func(t *testing.T) {
		root := t.TempDir()
		dbPath := filepath.Join(root, "state_5.sqlite")
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT); INSERT INTO threads VALUES ('t1', 'hello');"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		_ = db.Close()

		p := New(root, nil)
		det, err := p.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect error: %v", err)
		}
		if !det.Present {
			t.Errorf("Detect Present = false, want true (populated threads index indicates sessions)")
		}
		if len(det.Generations) != 1 || det.Generations[0] != "state:state_5.sqlite" {
			t.Errorf("Detect.Generations = %v, want [state:state_5.sqlite]", det.Generations)
		}
	})

	t.Run("corrupt threads index", func(t *testing.T) {
		root := t.TempDir()
		dbPath := filepath.Join(root, "state_5.sqlite")
		if err := os.WriteFile(dbPath, []byte("not a sqlite db"), 0o644); err != nil {
			t.Fatal(err)
		}

		p := New(root, nil)
		det, err := p.Detect(context.Background())
		if err != nil {
			t.Fatalf("Detect should not fail on unreadable index: %v", err)
		}
		if det.Present {
			t.Errorf("Detect Present = true, want false")
		}
		// Corrupt DB is noted in diagnostics notes
		foundNote := false
		for _, note := range det.Notes {
			if strings.Contains(note, "unavailable") {
				foundNote = true
				break
			}
		}
		if !foundNote {
			t.Errorf("expected note about unavailable index in det.Notes, got %v", det.Notes)
		}
	})
}

func TestDetect_ContextCanceled(t *testing.T) {
	root := t.TempDir()
	p := New(root, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Detect(ctx); err == nil {
		t.Fatal("Detect on canceled context returned nil error, want ctx.Err()")
	}
}

func TestWatchPaths(t *testing.T) {
	root := t.TempDir()
	p := New(root, nil)

	wp := p.WatchPaths()
	wantSessions := filepath.Join(root, "sessions")
	wantArchived := filepath.Join(root, "archived_sessions")
	if len(wp) != 2 || wp[0] != wantSessions || wp[1] != wantArchived {
		t.Fatalf("WatchPaths() = %v, want [%s, %s]", wp, wantSessions, wantArchived)
	}

	// With state_<N>.sqlite and -wal
	stateDB := filepath.Join(root, "state_3.sqlite")
	if err := os.WriteFile(stateDB, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	wal := stateDB + "-wal"
	if err := os.WriteFile(wal, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	wpWithDB := p.WatchPaths()
	if len(wpWithDB) != 4 {
		t.Fatalf("WatchPaths() len = %d, want 4 (%v)", len(wpWithDB), wpWithDB)
	}
	if wpWithDB[2] != stateDB || wpWithDB[3] != wal {
		t.Errorf("WatchPaths() state DB and WAL entries = [%s, %s], want [%s, %s]", wpWithDB[2], wpWithDB[3], stateDB, wal)
	}
}
