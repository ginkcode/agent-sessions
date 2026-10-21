package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func (s *stubRemoteBackend) CommitFileUpload(_ context.Context, dir, path, token string) error {
	if dir != "/project" || path != "sub/source.bin" {
		return errors.New("wrong destination")
	}
	if string(s.transport.artifacts[token]) != "payload" {
		return errors.New("wrong payload")
	}
	// Simulate a commit failure: app must still clean the staged artifact.
	return errors.New("destination exists")
}

func TestAddFileRemoteCleanupCancelAndHost(t *testing.T) {
	transport := newFakeArtifactTransport()
	a := setupRemoteApp(&stubRemoteBackend{transport: transport})
	a.SetArtifactTransportOverride(transport)
	source := filepath.Join(t.TempDir(), "source.bin")
	os.WriteFile(source, []byte("payload"), 0600)
	a.openFileDialogOverride = func(context.Context) (string, error) { return source, nil }
	if ok, err := a.AddFile("/project", "sub", "remote-server"); ok || err == nil {
		t.Fatalf("commit failure: %v %v", ok, err)
	}
	if len(transport.artifacts) != 0 {
		t.Fatal("upload leaked")
	}
	a.openFileDialogOverride = func(context.Context) (string, error) { return "", nil }
	if ok, err := a.AddFile("/project", "sub", "remote-server"); ok || err != nil {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	a.openFileDialogOverride = func(context.Context) (string, error) { t.Fatal("dialog opened for wrong host"); return "", nil }
	if ok, err := a.AddFile("/project", "sub", ""); ok || !errors.Is(err, ErrSaveHostChanged) {
		t.Fatalf("host: %v %v", ok, err)
	}
}
