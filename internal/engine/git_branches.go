package engine

import (
	"context"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// GitBranchesBackend is optional so older host backends remain compatible.
type GitBranchesBackend interface {
	GitBranches(context.Context, string) (files.GitBranchList, error)
	GitSwitch(context.Context, string, files.GitSwitchRequest) error
}

func (e *Engine) GitBranches(ctx context.Context, dir string) (files.GitBranchList, error) {
	if !e.svc.knownDirectory(dir) {
		return files.GitBranchList{}, fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitBranches(ctx, dir)
}
func (e *Engine) GitSwitch(ctx context.Context, dir string, req files.GitSwitchRequest) error {
	if !e.svc.knownDirectory(dir) {
		return fmt.Errorf("%w: %s", ErrUnknownDirectory, dir)
	}
	return files.GitSwitch(ctx, dir, req)
}
