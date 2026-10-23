package engine

import (
	"context"
	"fmt"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func (e *Engine) GitStatus(ctx context.Context, dir string) (files.RepoStatus, error) {
	return e.svc.GitStatus(ctx, dir)
}

func (s *Service) GitStatus(ctx context.Context, dir string) (files.RepoStatus, error) {
	if !s.knownDirectory(dir) {
		return files.RepoStatus{}, fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitRepoStatus(ctx, dir)
}
