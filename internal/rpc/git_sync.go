package rpc

import (
	"context"
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type GitSyncParams struct {
	Dir     string               `json:"dir"`
	Request files.GitSyncRequest `json:"request"`
}

// GitSync is canceled through ctx: call sends $/cancelRequest, which cancels
// the server's request context and kills the Git process group.
func (c *Client) GitSync(ctx context.Context, dir string, req files.GitSyncRequest) (files.GitSyncResult, error) {
	if !c.caps.GitSync {
		return files.GitSyncResult{Op: req.Op}, engine.ErrUnsupportedAction
	}
	var out files.GitSyncResult
	err := c.call(ctx, "gitSync", GitSyncParams{dir, req}, &out)
	return out, err
}

func (s *Server) dispatchGitSync(ctx context.Context, req Request, backend engine.Backend) (any, error) {
	s.mu.Lock()
	enabled := s.gitSync
	s.mu.Unlock()
	b, ok := backend.(engine.GitSyncBackend)
	if !enabled || !ok {
		return nil, engine.ErrUnsupportedAction
	}
	var p GitSyncParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
	}
	return b.GitSync(ctx, p.Dir, p.Request)
}
