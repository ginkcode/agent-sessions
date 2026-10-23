package engine

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/files"
)

type GitDiffBackend interface {
	ReadGitDiff(context.Context, string, string, files.GitDiffBasis) (files.GitDiffContent, error)
}

func (e *Engine) ReadGitDiff(ctx context.Context, dir, path string, basis files.GitDiffBasis) (files.GitDiffContent, error) {
	if !e.svc.knownDirectory(dir) {
		return files.GitDiffContent{}, fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.ReadGitDiff(ctx, e.editPolicy(), dir, path, basis)
}
