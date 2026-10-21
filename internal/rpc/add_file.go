package rpc

import (
	"context"
	"errors"
)

// AddFile is local-only: remote callers must upload a token, never name a server source.
func (c *Client) AddFile(context.Context, string, string, string) error {
	return errors.New("remote file addition requires an upload")
}

func (c *Client) CommitFileUpload(ctx context.Context, dir, path, token string) error {
	if !c.Capabilities().AddFile {
		return errors.New("remote host does not support adding files")
	}
	return c.call(ctx, "commitFileUpload", FileDownloadParams{Dir: dir, Path: path, Token: token}, nil)
}
