package rpc

import (
	"context"
	"fmt"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

func (c *Client) GitStatus(ctx context.Context, dir string) (files.RepoStatus, error) {
	if !c.caps.GitChanges {
		return files.RepoStatus{}, fmt.Errorf("%w: this host's agent-sessions cannot report Git changes; update it", engine.ErrUnsupportedAction)
	}
	var out files.RepoStatus
	err := c.call(ctx, "gitStatus", GitStatusParams{Dir: dir}, &out)
	return out, err
}
