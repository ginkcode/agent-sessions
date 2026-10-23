package rpc

import (
	"context"
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type GitPathsParams struct {
	Dir   string   `json:"dir"`
	Paths []string `json:"paths"`
}
type GitDiscardParams struct {
	Dir     string                  `json:"dir"`
	Request files.GitDiscardRequest `json:"request"`
}
type GitCommitParams struct {
	Dir     string                 `json:"dir"`
	Request files.GitCommitRequest `json:"request"`
}

func (c *Client) GitStage(ctx context.Context, dir string, paths []string) error {
	if !c.caps.GitWrite {
		return engine.ErrUnsupportedAction
	}
	return c.call(ctx, "gitStage", GitPathsParams{dir, paths}, nil)
}
func (c *Client) GitUnstage(ctx context.Context, dir string, paths []string) error {
	if !c.caps.GitWrite {
		return engine.ErrUnsupportedAction
	}
	return c.call(ctx, "gitUnstage", GitPathsParams{dir, paths}, nil)
}
func (c *Client) GitDiscard(ctx context.Context, dir string, req files.GitDiscardRequest) error {
	if !c.caps.GitWrite {
		return engine.ErrUnsupportedAction
	}
	return c.call(ctx, "gitDiscard", GitDiscardParams{dir, req}, nil)
}
func (c *Client) GitCommit(ctx context.Context, dir string, req files.GitCommitRequest) error {
	if !c.caps.GitWrite {
		return engine.ErrUnsupportedAction
	}
	return c.call(ctx, "gitCommit", GitCommitParams{dir, req}, nil)
}
func (s *Server) dispatchGitWrite(ctx context.Context, req Request, backend engine.Backend) (any, error) {
	s.mu.Lock()
	enabled := s.gitWrite
	s.mu.Unlock()
	if !enabled {
		return nil, engine.ErrUnsupportedAction
	}
	b, ok := backend.(engine.GitWriteBackend)
	if !ok {
		return nil, engine.ErrUnsupportedAction
	}
	invalid := func(err error) (any, error) {
		return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
	}
	switch req.Method {
	case "gitStage", "gitUnstage":
		var p GitPathsParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return invalid(err)
		}
		if req.Method == "gitStage" {
			return nil, b.GitStage(ctx, p.Dir, p.Paths)
		}
		return nil, b.GitUnstage(ctx, p.Dir, p.Paths)
	case "gitDiscard":
		var p GitDiscardParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return invalid(err)
		}
		return nil, b.GitDiscard(ctx, p.Dir, p.Request)
	default:
		var p GitCommitParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return invalid(err)
		}
		return nil, b.GitCommit(ctx, p.Dir, p.Request)
	}
}
