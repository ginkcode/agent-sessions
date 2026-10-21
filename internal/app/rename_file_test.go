package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func (s *stubRemoteBackend) RenameFile(_ context.Context, dir, path, name string) error {
	if dir != "/project" || path != "folder/file" || name != "new" {
		return errors.New("wrong rename arguments")
	}
	return nil
}

func TestRenameFileHostRouting(t *testing.T) {
	transport := newFakeArtifactTransport()
	a := setupRemoteApp(&stubRemoteBackend{transport: transport})
	a.SetArtifactTransportOverride(transport)
	if err := a.RenameFile("/project", "folder/file", "new", ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("wrong host: %v", err)
	}
	if err := a.RenameFile("/project", "folder/file", "new", "remote-server"); err != nil {
		t.Fatal(err)
	}
}

func TestRenameFileOffline(t *testing.T) {
	b := offlineBackend{}
	if err := b.RenameFile(context.Background(), "root", "file", "new"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("offline: %v", err)
	}
}
