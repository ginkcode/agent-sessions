package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestGitWriteAuthorization(t *testing.T) {
	e := editEngine(t.TempDir())
	dir := t.TempDir()
	ctx := context.Background()
	for _, err := range []error{e.GitStage(ctx, dir, nil), e.GitUnstage(ctx, dir, nil), e.GitDiscard(ctx, dir, files.GitDiscardRequest{}), e.GitCommit(ctx, dir, files.GitCommitRequest{Message: "commit"})} {
		if !errors.Is(err, ErrUnknownDirectory) {
			t.Fatalf("unknown directory: %v", err)
		}
	}
}
