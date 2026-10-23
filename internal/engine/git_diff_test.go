package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestReadGitDiffAuthorization(t *testing.T) {
	e := editEngine(t.TempDir())
	if _, err := e.ReadGitDiff(context.Background(), t.TempDir(), "file", files.GitDiffStaged); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatal(err)
	}
}
