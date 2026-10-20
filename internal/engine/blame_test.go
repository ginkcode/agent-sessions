package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestBlameFileAuthorization(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	e := editEngine(root)
	writeEditFile(t, root, "file.txt", "text\n")
	writeEditFile(t, other, "file.txt", "secret\n")
	if _, err := e.BlameFile(context.Background(), other, "file.txt", 1); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("unknown directory: %v", err)
	}
	if _, err := e.BlameFile(context.Background(), root, "../secret", 1); !errors.Is(err, files.ErrOutsideRoot) {
		t.Fatalf("outside root: %v", err)
	}
	if _, err := e.BlameFile(context.Background(), root, "file.txt", 0); err == nil {
		t.Fatal("invalid line accepted")
	}
}
