package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func (s *stubRemoteBackend) CreateDirectory(_ context.Context, dir, path, name string) error {
	if dir != "/project" || path != "folder/file" || name != "new" {
		return errors.New("wrong create directory arguments")
	}
	return nil
}

func (s *stubRemoteBackend) CreateFile(_ context.Context, dir, path, name string) error {
	if dir != "/project" || path != "folder" || name != "new.go" {
		return errors.New("wrong create file arguments")
	}
	return nil
}

func TestCreateDirectoryHostRouting(t *testing.T) {
	transport := newFakeArtifactTransport()
	a := setupRemoteApp(&stubRemoteBackend{transport: transport})
	a.SetArtifactTransportOverride(transport)
	if err := a.CreateDirectory("/project", "folder/file", "new", ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("wrong host: %v", err)
	}
	if err := a.CreateDirectory("/project", "folder/file", "new", "remote-server"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateFile("/project", "folder", "new.go", ""); !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("wrong host: %v", err)
	}
	if err := a.CreateFile("/project", "folder", "new.go", "remote-server"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDirectoryOffline(t *testing.T) {
	b := offlineBackend{}
	if err := b.CreateDirectory(context.Background(), "root", "file", "new"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("offline: %v", err)
	}
	if err := b.CreateFile(context.Background(), "root", "file", "new"); !errors.Is(err, rpc.ErrDisconnected) {
		t.Fatalf("offline: %v", err)
	}
}
