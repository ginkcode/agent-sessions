package rpc

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type gitDiffMock struct {
	*mockBackend
	dir, path string
	basis     files.GitDiffBasis
}

func (b *gitDiffMock) ReadGitDiff(_ context.Context, dir, path string, basis files.GitDiffBasis) (files.GitDiffContent, error) {
	b.dir, b.path, b.basis = dir, path, basis
	return files.GitDiffContent{Content: files.Content{Path: path, Text: "index\n", Changes: []files.LineChange{{Kind: "modified", Start: 1, Count: 1, Removed: []string{"head"}}}, Edit: &files.EditInfo{Reason: files.EditReadOnly}}, Basis: basis, ReadOnly: true}, b.errToReturn
}
func TestReadGitDiffRPC(t *testing.T) {
	b := &gitDiffMock{mockBackend: &mockBackend{}}
	_, c, cleanup := setupPipePair(b)
	defer cleanup()
	init, err := c.Initialize(context.Background(), InitializeRequest{})
	if err != nil || !init.Capabilities.GitDiff {
		t.Fatalf("init %+v %v", init, err)
	}
	got, err := c.ReadGitDiff(context.Background(), "/repo/sub", "literal[1]", files.GitDiffStaged)
	want, _ := b.ReadGitDiff(context.Background(), "/repo/sub", "literal[1]", files.GitDiffStaged)
	if err != nil || !reflect.DeepEqual(got, want) || b.dir != "/repo/sub" || b.path != "literal[1]" || b.basis != files.GitDiffStaged {
		t.Fatalf("read %+v %v", got, err)
	}
	b.errToReturn = engine.ErrUnknownDirectory
	if _, err := c.ReadGitDiff(context.Background(), "/unknown", "file", files.GitDiffIndex); err == nil {
		t.Fatal("lost error")
	}
}
func TestReadGitDiffUnsupported(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	rec := &recordingWriter{}
	c := NewClient(r, rec)
	defer c.Close()
	if _, err := c.ReadGitDiff(context.Background(), "/repo", "file", files.GitDiffHead); err == nil {
		t.Fatal("old backend accepted")
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported request sent")
	}
}
