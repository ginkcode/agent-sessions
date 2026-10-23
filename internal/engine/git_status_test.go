package engine

import (
	"context"
	"errors"
	"testing"
)

func TestGitStatusAuthorization(t *testing.T) {
	root := t.TempDir()
	e := editEngine(root)
	if _, err := e.GitStatus(context.Background(), t.TempDir()); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("unknown directory: %v", err)
	}
	got, err := e.GitStatus(context.Background(), root)
	if err != nil || got.OK || got.Reason == "" {
		t.Fatalf("known nonrepo: %+v %v", got, err)
	}
}
