package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func TestGitSyncAuthorization(t *testing.T) {
	e := editEngine(t.TempDir())
	var _ GitSyncBackend = e
	if _, err := e.GitSync(context.Background(), t.TempDir(), files.GitSyncRequest{Op: files.GitSyncFetch}); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatal(err)
	}
}
