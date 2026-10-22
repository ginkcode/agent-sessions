package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func (s *stubRemoteBackend) DeleteFile(_ context.Context, dir, path string) error {
	if dir != "/project" || path != "folder/file" {
		return errors.New("wrong delete arguments")
	}
	return nil
}

func TestDeleteFileHostRouting(t *testing.T) {
	transport := newFakeArtifactTransport()
	a := setupRemoteApp(&stubRemoteBackend{transport: transport})
	a.SetArtifactTransportOverride(transport)
	if err := a.DeleteFile("/project", "folder/file", ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("wrong host: %v", err)
	}
	if err := a.DeleteFile("/project", "folder/file", "remote-server"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteFileOffline(t *testing.T) {
	b := offlineBackend{}
	if err := b.DeleteFile(context.Background(), "root", "file"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("offline: %v", err)
	}
}
