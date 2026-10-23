package rpc

import (
	"context"
	"errors"
)

func (c *Client) CreateDirectory(ctx context.Context, dir, path, name string) error {
	if !c.Capabilities().CreateDirectory {
		return errors.New("remote host does not support creating directories")
	}
	return c.call(ctx, "createDirectory", CreateDirectoryParams{Dir: dir, Path: path, Name: name}, nil)
}

func (c *Client) CreateFile(ctx context.Context, dir, path, name string) error {
	if !c.Capabilities().CreateFile {
		return errors.New("remote host does not support creating files")
	}
	return c.call(ctx, "createFile", CreateDirectoryParams{Dir: dir, Path: path, Name: name}, nil)
}

// CreateDirectoryParams also carries createFile requests.
type CreateDirectoryParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
	Name string `json:"name"`
}
