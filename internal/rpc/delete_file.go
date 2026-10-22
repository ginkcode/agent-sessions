package rpc

import (
	"context"
	"errors"
)

func (c *Client) DeleteFile(ctx context.Context, dir, path string) error {
	if !c.Capabilities().DeleteFile {
		return errors.New("remote host does not support deleting files")
	}
	return c.call(ctx, "deleteFile", DeleteFileParams{Dir: dir, Path: path}, nil)
}

type DeleteFileParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
}
