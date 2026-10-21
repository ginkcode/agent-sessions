package rpc

import (
	"context"
	"errors"
)

func (c *Client) RenameFile(ctx context.Context, dir, path, name string) error {
	if !c.Capabilities().RenameFile {
		return errors.New("remote host does not support renaming files")
	}
	return c.call(ctx, "renameFile", RenameFileParams{Dir: dir, Path: path, Name: name}, nil)
}

type RenameFileParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
	Name string `json:"name"`
}
