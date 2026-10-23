package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestSearchFilesAuthorization(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	e := editEngine(root)
	writeEditFile(t, root, "file.txt", "needle\n")
	writeEditFile(t, other, "file.txt", "needle\n")
	q := files.SearchQuery{Pattern: "needle"}
	if _, err := e.SearchFiles(context.Background(), other, q); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("unknown directory: %v", err)
	}
	got, err := e.SearchFiles(context.Background(), root, q)
	if err != nil || got.Matches != 1 || got.Files[0].Path != "file.txt" {
		t.Fatalf("search = %+v, %v", got, err)
	}
}
