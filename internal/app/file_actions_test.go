package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
)

func (s *stubRemoteBackend) PrepareFileDownload(_ context.Context, dir, path, token string) (files.Download, error) {
	s.transport.artifacts[token] = s.exportArtifactData
	return files.Download{Name: filepath.Base(path), Token: token}, nil
}

func TestRemoteDownloadFile(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "cancel"}[cancel], func(t *testing.T) {
			transport := newFakeArtifactTransport()
			stub := &stubRemoteBackend{transport: transport, exportArtifactData: []byte{0, 255, 42}}
			a := setupRemoteApp(stub)
			a.SetArtifactTransportOverride(transport)
			dest := filepath.Join(t.TempDir(), "saved.bin")
			a.saveDialogOverride = func(_ context.Context, name string) (string, error) {
				if name != "file.bin" {
					t.Fatalf("name %q", name)
				}
				if cancel {
					return "", nil
				}
				return dest, nil
			}
			result, err := a.DownloadFile("/project", "file.bin")
			if err != nil {
				t.Fatal(err)
			}
			if len(transport.artifacts) != 0 {
				t.Fatal("staging artifact leaked")
			}
			if cancel {
				if result != "" {
					t.Fatalf("cancel result %q", result)
				}
				return
			}
			data, err := os.ReadFile(dest)
			if err != nil || string(data) != string(stub.exportArtifactData) || result != dest {
				t.Fatalf("download %q %v", data, err)
			}
		})
	}
}

func (s *stubRemoteBackend) ResolveTerminalFile(_ context.Context, dir, path string) (files.TerminalFile, error) {
	return files.TerminalFile{Dir: dir, Path: path}, nil
}

func TestResolveTerminalFileRoutesToSelectedHost(t *testing.T) {
	a := setupRemoteApp(&stubRemoteBackend{})
	// These are opaque remote paths, not desktop filesystem paths.
	got, err := a.ResolveTerminalFile(`C:\remote\project`, `sub\Makefile`)
	if err != nil || got.Dir != `C:\remote\project` || got.Path != `sub\Makefile` {
		t.Fatalf("remote resolution: %+v %v", got, err)
	}
}

func TestRemoteNativeFileActionsRejected(t *testing.T) {
	a := setupRemoteApp(&stubRemoteBackend{})
	if err := a.CopyFile("/project", "file"); err == nil {
		t.Fatal("remote copy accepted")
	}
	if err := a.RevealFile("/project", "file"); err == nil {
		t.Fatal("remote reveal accepted")
	}
}
