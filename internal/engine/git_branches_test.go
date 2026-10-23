package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestGitBranchesAuthorization(t *testing.T) {
	e := editEngine(t.TempDir())
	other := t.TempDir()
	if _, err := e.GitBranches(context.Background(), other); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatal(err)
	}
	if err := e.GitSwitch(context.Background(), other, files.GitSwitchRequest{Name: "new", Create: true}); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatal(err)
	}
}
