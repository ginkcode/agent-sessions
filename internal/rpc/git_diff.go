package rpc

import (
	"context"
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type ReadGitDiffParams struct {
	Dir   string             `json:"dir"`
	Path  string             `json:"path"`
	Basis files.GitDiffBasis `json:"basis"`
}

func (c *Client) ReadGitDiff(ctx context.Context, dir, path string, basis files.GitDiffBasis) (files.GitDiffContent, error) {
	if !c.caps.GitDiff {
		return files.GitDiffContent{}, engine.ErrUnsupportedAction
	}
	var out files.GitDiffContent
	err := c.call(ctx, "readGitDiff", ReadGitDiffParams{dir, path, basis}, &out)
	return out, err
}
func (s *Server) dispatchGitDiff(ctx context.Context, req Request, backend engine.Backend) (any, error) {
	b, ok := backend.(engine.GitDiffBackend)
	if !ok {
		return nil, engine.ErrUnsupportedAction
	}
	var p ReadGitDiffParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ResponseError{Code: CodeInvalidParams, Message: err.Error()}
	}
	return b.ReadGitDiff(ctx, p.Dir, p.Path, p.Basis)
}
