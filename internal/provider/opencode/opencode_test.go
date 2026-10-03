package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestNew(t *testing.T) {
	t.Run("default git resolver", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "custom", "root")
		p := New(root, nil)
		if p.root != root {
			t.Errorf("root = %q, want %q", p.root, root)
		}
		if p.git == nil {
			t.Errorf("git is nil, expected default GitResolver")
		}
	})

	t.Run("custom git resolver and clean path", func(t *testing.T) {
		git := pathutil.NewGitResolver()
		base := t.TempDir()
		sep := string(filepath.Separator)
		p := New(base+sep+"custom"+sep+"root"+sep+".."+sep+"root2"+sep, git)
		if want := filepath.Join(base, "custom", "root2"); p.root != want {
			t.Errorf("root = %q, want %q", p.root, want)
		}
		if p.git != git {
			t.Errorf("git resolver not preserved")
		}
	})
}

func TestIDAndDisplayName(t *testing.T) {
	p := New(t.TempDir(), nil)
	if p.ID() != model.AgentOpenCode {
		t.Errorf("ID = %q, want %q", p.ID(), model.AgentOpenCode)
	}
	if p.DisplayName() != "OpenCode" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName(), "OpenCode")
	}
	// Interface verification
	var _ provider.Provider = p
}

func TestResumeCommand(t *testing.T) {
	p := New(t.TempDir(), nil)
	meta := model.SessionMeta{
		Ref: model.SessionRef{
			Agent: model.AgentOpenCode,
			ID:    "session-xyz-123",
		},
		CWD: "/home/developer/workspace/app",
	}

	cmd := p.ResumeCommand(meta)
	expectedArgv := []string{"opencode", "--session", "session-xyz-123"}
	if len(cmd.Argv) != len(expectedArgv) {
		t.Fatalf("Argv len = %d, want %d", len(cmd.Argv), len(expectedArgv))
	}
	for i, v := range cmd.Argv {
		if v != expectedArgv[i] {
			t.Errorf("Argv[%d] = %q, want %q", i, v, expectedArgv[i])
		}
	}
	if cmd.Dir != meta.CWD {
		t.Errorf("Dir = %q, want %q", cmd.Dir, meta.CWD)
	}
}

func TestWatchPaths(t *testing.T) {
	t.Run("empty or absent root returns nil or empty", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "nonexistent")
		p := New(root, nil)
		paths := p.WatchPaths()
		if len(paths) != 0 {
			t.Errorf("WatchPaths = %v, want empty", paths)
		}
	})

	t.Run("only existing paths among db wal and legacy storage", func(t *testing.T) {
		root := t.TempDir()
		p := New(root, nil)

		// Create only opencode.db and storage/session
		dbPath := filepath.Join(root, "opencode.db")
		if err := os.WriteFile(dbPath, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		sessDir := filepath.Join(root, "storage", "session")
		if err := os.MkdirAll(sessDir, 0o755); err != nil {
			t.Fatal(err)
		}

		paths := p.WatchPaths()
		want := []string{dbPath, sessDir}
		if len(paths) != len(want) {
			t.Fatalf("WatchPaths len = %d, want %d: %v", len(paths), len(want), paths)
		}
		for i, v := range paths {
			if v != want[i] {
				t.Errorf("WatchPaths[%d] = %q, want %q", i, v, want[i])
			}
		}

		// Now create -wal and storage/message
		walPath := dbPath + "-wal"
		if err := os.WriteFile(walPath, []byte("wal"), 0o644); err != nil {
			t.Fatal(err)
		}
		msgDir := filepath.Join(root, "storage", "message")
		if err := os.MkdirAll(msgDir, 0o755); err != nil {
			t.Fatal(err)
		}

		paths2 := p.WatchPaths()
		want2 := []string{dbPath, walPath, sessDir, msgDir}
		if len(paths2) != len(want2) {
			t.Fatalf("WatchPaths len = %d, want %d: %v", len(paths2), len(want2), paths2)
		}
		for i, v := range paths2 {
			if v != want2[i] {
				t.Errorf("WatchPaths[%d] = %q, want %q", i, v, want2[i])
			}
		}
	})
}

func TestStubsReportUnsupported(t *testing.T) {
	p := New(t.TempDir(), nil)
	ctx := t.Context()

	// Load stub
	_, err := p.Load(ctx, model.SessionRef{Agent: model.AgentOpenCode, ID: "123"})
	if err == nil {
		t.Fatalf("Load expected error, got nil")
	}
	if !errors.Is(err, ErrUnsupportedLoad) {
		t.Errorf("Load error = %v, expected to match ErrUnsupportedLoad", err)
	}
	if !strings.Contains(err.Error(), "M1-06") {
		t.Errorf("Load error %q should mention M1-06", err.Error())
	}

	// Blob stub
	_, err = p.Blob(ctx, model.SessionRef{Agent: model.AgentOpenCode, ID: "123"}, "key")
	if err == nil {
		t.Fatalf("Blob expected error, got nil")
	}
	if !errors.Is(err, ErrUnsupportedBlob) {
		t.Errorf("Blob error = %v, expected to match ErrUnsupportedBlob", err)
	}
	if !strings.Contains(err.Error(), "M1-06") {
		t.Errorf("Blob error %q should mention M1-06", err.Error())
	}
}
