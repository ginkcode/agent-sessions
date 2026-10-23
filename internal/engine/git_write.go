package engine

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// GitWriteBackend is an optional extension, advertised only on writable hosts.
type GitWriteBackend interface {
	GitStage(context.Context, string, []string) error
	GitUnstage(context.Context, string, []string) error
	GitDiscard(context.Context, string, files.GitDiscardRequest) error
	GitCommit(context.Context, string, files.GitCommitRequest) error
}

func (e *Engine) GitStage(ctx context.Context, dir string, paths []string) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitStage(ctx, dir, paths)
}
func (e *Engine) GitUnstage(ctx context.Context, dir string, paths []string) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitUnstage(ctx, dir, paths)
}
func (e *Engine) GitDiscard(ctx context.Context, dir string, req files.GitDiscardRequest) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitDiscard(ctx, e.editPolicy(), dir, req)
}
func (e *Engine) GitCommit(ctx context.Context, dir string, req files.GitCommitRequest) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitCommit(ctx, dir, req)
}
