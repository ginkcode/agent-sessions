package rpc

import (
	"context"
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type GitSwitchParams struct {
	Dir     string                 `json:"dir"`
	Request files.GitSwitchRequest `json:"request"`
}

func (c *Client) GitBranches(ctx context.Context, dir string) (files.GitBranchList, error) {
	if !c.caps.GitChanges {
		return files.GitBranchList{}, engine.ErrUnsupportedAction
	}
	var out files.GitBranchList
	err := c.call(ctx, "gitBranches", GitStatusParams{Dir: dir}, &out)
	return out, err
}
func (c *Client) GitSwitch(ctx context.Context, dir string, req files.GitSwitchRequest) error {
	if !c.caps.GitWrite {
		return engine.ErrUnsupportedAction
	}
	return c.call(ctx, "gitSwitch", GitSwitchParams{dir, req}, nil)
}
func (s *Server) dispatchGitBranches(ctx context.Context, req Request, backend engine.Backend) (any, error) {
	b, ok := backend.(engine.GitBranchesBackend)
	if !ok {
		return nil, engine.ErrUnsupportedAction
	}
	if req.Method == "gitBranches" {
		var p GitStatusParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
		}
		return b.GitBranches(ctx, p.Dir)
	}
	s.mu.Lock()
	enabled := s.gitWrite
	s.mu.Unlock()
	if !enabled {
		return nil, engine.ErrUnsupportedAction
	}
	var p GitSwitchParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
	}
	return nil, b.GitSwitch(ctx, p.Dir, p.Request)
}
