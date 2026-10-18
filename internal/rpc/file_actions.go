package rpc

import (
	"context"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type FileDownloadParams struct {
	Dir   string `json:"dir"`
	Path  string `json:"path"`
	Token string `json:"token"`
}

func (c *Client) ResolveFile(ctx context.Context, dir, path string) (string, error) {
	var out string
	err := c.call(ctx, "resolveFile", ReadFileParams{Dir: dir, Path: path}, &out)
	return out, err
}
func (c *Client) ResolveTerminalFile(ctx context.Context, dir, path string) (files.TerminalFile, error) {
	var out files.TerminalFile
	err := c.call(ctx, "resolveTerminalFile", ReadFileParams{Dir: dir, Path: path}, &out)
	return out, err
}

func (c *Client) PrepareFileDownload(ctx context.Context, dir, path, token string) (files.Download, error) {
	var out files.Download
	err := c.call(ctx, "prepareFileDownload", FileDownloadParams{Dir: dir, Path: path, Token: token}, &out)
	return out, err
}
