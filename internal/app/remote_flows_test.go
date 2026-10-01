package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
)

type stubRemoteBackend struct {
	engine.Backend

	resumeCmd            string
	handoffCmd           string
	bundleCmd            string
	preview              engine.HandoffPreview
	bundlePreview        engine.HandoffPreview
	renderHandoff        string
	renderHandoffCalled  bool
	saveHandoffCalled    bool
	renderBundleHandoff  string
	renderBundleCalled   bool
	exportCalled         bool
	exportDestPath       string
	openBundleCalled     bool
	openBundlePath       string
	openBundleSummary    engine.BundleSummary
	exportArtifactData   []byte
	transport            *fakeArtifactTransport
}

func (s *stubRemoteBackend) GetSessionMeta(_ context.Context, ref model.SessionRef) (model.SessionMeta, error) {
	return model.SessionMeta{Ref: ref, CWD: "/home/remote/remote-project"}, nil
}

func (s *stubRemoteBackend) CopyResumeCommand(_ context.Context, _ model.SessionRef) (string, error) {
	return s.resumeCmd, nil
}

func (s *stubRemoteBackend) RevealSource(_ context.Context, _ model.SessionRef) (string, error) {
	return "/remote/path/to/source", nil
}

func (s *stubRemoteBackend) HandoffCommand(_ context.Context, _ engine.HandoffRequest) (string, error) {
	return s.handoffCmd, nil
}

func (s *stubRemoteBackend) BuildHandoff(_ context.Context, _ engine.HandoffRequest) (engine.HandoffPreview, error) {
	return s.preview, nil
}

func (s *stubRemoteBackend) BundleHandoffCommand(_ context.Context, _ engine.BundleHandoffRequest) (string, error) {
	return s.bundleCmd, nil
}

func (s *stubRemoteBackend) BuildBundleHandoff(_ context.Context, _ engine.BundleHandoffRequest) (engine.HandoffPreview, error) {
	return s.bundlePreview, nil
}

func (s *stubRemoteBackend) RenderHandoff(_ context.Context, _ engine.HandoffRequest) (string, error) {
	s.renderHandoffCalled = true
	return s.renderHandoff, nil
}

func (s *stubRemoteBackend) SaveHandoff(_ context.Context, _ engine.HandoffRequest, dest string) (string, error) {
	s.saveHandoffCalled = true
	return dest, nil
}

func (s *stubRemoteBackend) RenderBundleHandoff(_ context.Context, _ engine.BundleHandoffRequest) (string, error) {
	s.renderBundleCalled = true
	return s.renderBundleHandoff, nil
}

func (s *stubRemoteBackend) ExportBundle(_ context.Context, _ engine.ExportRequest, destPath string) (string, error) {
	s.exportCalled = true
	s.exportDestPath = destPath
	if s.transport != nil && s.exportArtifactData != nil {
		// Simulate remote server writing to staging path
		parts := strings.Split(destPath, "/")
		token := parts[len(parts)-1]
		s.transport.artifacts[token] = s.exportArtifactData
	}
	return destPath, nil
}

func (s *stubRemoteBackend) OpenBundle(_ context.Context, path string) (engine.BundleSummary, error) {
	s.openBundleCalled = true
	s.openBundlePath = path
	return s.openBundleSummary, nil
}

type fakeArtifactTransport struct {
	artifacts map[string][]byte
}

func newFakeArtifactTransport() *fakeArtifactTransport {
	return &fakeArtifactTransport{artifacts: make(map[string][]byte)}
}

func (f *fakeArtifactTransport) PutArtifact(_ context.Context, token string, src io.Reader) (string, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	f.artifacts[token] = data
	return "sha256:dummy", nil
}

func (f *fakeArtifactTransport) GetArtifact(_ context.Context, token string, remove bool, dst io.Writer) error {
	data, ok := f.artifacts[token]
	if !ok {
		return errors.New("artifact not found")
	}
	if _, err := dst.Write(data); err != nil {
		return err
	}
	if remove {
		delete(f.artifacts, token)
	}
	return nil
}

func (f *fakeArtifactTransport) StagingPath(token string) string {
	return "/remote/cache/staging/" + token
}

func setupRemoteApp(stub *stubRemoteBackend) *App {
	a := NewApp()
	a.conn = newConnection(nil)
	a.conn.wantHost = "remote-server"
	a.conn.localActive = false
	a.conn.client = stub
	a.conn.state = ConnectionState{
		Phase:      ConnConnected,
		Host:       "remote-server",
		Generation: 1,
	}
	return a
}

func TestRemote_CommandWrappingAndRevealSource(t *testing.T) {
	stub := &stubRemoteBackend{
		resumeCmd:     "claude resume 'session 123'",
		handoffCmd:    "cd /remote/cwd && opencode",
		bundleCmd:     "cd /remote/bundle && codex",
		preview:       engine.HandoffPreview{Command: "claude --prompt foo"},
		bundlePreview: engine.HandoffPreview{Command: "codex --handoff bar"},
	}
	a := setupRemoteApp(stub)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "session-1"}

	// 1. CopyResumeCommand is returned as built on the remote host, for an
	// interactive shell there; no ssh wrapping.
	cmd, err := a.CopyResumeCommand(ref)
	if err != nil {
		t.Fatalf("CopyResumeCommand: %v", err)
	}
	wantResume := "claude resume 'session 123'"
	if cmd != wantResume {
		t.Errorf("CopyResumeCommand = %q, want %q", cmd, wantResume)
	}

	// 2. RevealSource returns explicit error on remote
	err = a.RevealSource(ref)
	if err == nil || !strings.Contains(err.Error(), "not supported on remote") {
		t.Errorf("RevealSource err = %v, want 'not supported on remote hosts'", err)
	}

	// 3. HandoffCommand unwrapped
	hCmd, err := a.HandoffCommand(engine.HandoffRequest{Ref: ref})
	if err != nil {
		t.Fatalf("HandoffCommand: %v", err)
	}
	wantHandoff := "cd /remote/cwd && opencode"
	if hCmd != wantHandoff {
		t.Errorf("HandoffCommand = %q, want %q", hCmd, wantHandoff)
	}

	// 4. BuildHandoff preview.Command unwrapped
	p, err := a.BuildHandoff(engine.HandoffRequest{Ref: ref})
	if err != nil {
		t.Fatalf("BuildHandoff: %v", err)
	}
	wantPreview := "claude --prompt foo"
	if p.Command != wantPreview {
		t.Errorf("BuildHandoff Command = %q, want %q", p.Command, wantPreview)
	}

	// 5. BundleHandoffCommand unwrapped
	bCmd, err := a.BundleHandoffCommand(engine.BundleHandoffRequest{BundleID: "b-1"})
	if err != nil {
		t.Fatalf("BundleHandoffCommand: %v", err)
	}
	wantBundle := "cd /remote/bundle && codex"
	if bCmd != wantBundle {
		t.Errorf("BundleHandoffCommand = %q, want %q", bCmd, wantBundle)
	}

	// 6. BuildBundleHandoff preview.Command unwrapped
	bp, err := a.BuildBundleHandoff(engine.BundleHandoffRequest{BundleID: "b-1"})
	if err != nil {
		t.Fatalf("BuildBundleHandoff: %v", err)
	}
	wantBundlePreview := "codex --handoff bar"
	if bp.Command != wantBundlePreview {
		t.Errorf("BuildBundleHandoff Command = %q, want %q", bp.Command, wantBundlePreview)
	}
}

func TestRemote_SaveHandoff(t *testing.T) {
	stub := &stubRemoteBackend{
		renderHandoff:       "# Rendered Remote Handoff\nContent here",
		renderBundleHandoff: "# Rendered Bundle Handoff\nContent here",
	}
	a := setupRemoteApp(stub)

	tmpDir := t.TempDir()

	// 1. SaveHandoff writes locally with RenderHandoff
	dest := filepath.Join(tmpDir, "handoff.md")
	a.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		return dest, nil
	}

	path, err := a.SaveHandoff(engine.HandoffRequest{})
	if err != nil {
		t.Fatalf("SaveHandoff: %v", err)
	}
	if path != dest {
		t.Errorf("SaveHandoff path = %q, want %q", path, dest)
	}
	if !stub.renderHandoffCalled {
		t.Error("expected RenderHandoff to be called")
	}
	if stub.saveHandoffCalled {
		t.Error("did not expect backend SaveHandoff to be called on remote")
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != stub.renderHandoff {
		t.Errorf("content = %q, want %q", string(data), stub.renderHandoff)
	}
	info, _ := os.Stat(dest)
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	// 2. SaveBundleHandoff writes locally with RenderBundleHandoff
	bundleDest := filepath.Join(tmpDir, "bundle-handoff.md")
	a.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		return bundleDest, nil
	}

	path, err = a.SaveBundleHandoff(engine.BundleHandoffRequest{})
	if err != nil {
		t.Fatalf("SaveBundleHandoff: %v", err)
	}
	if path != bundleDest {
		t.Errorf("SaveBundleHandoff path = %q, want %q", path, bundleDest)
	}
	if !stub.renderBundleCalled {
		t.Error("expected RenderBundleHandoff to be called")
	}

	data, err = os.ReadFile(bundleDest)
	if err != nil {
		t.Fatalf("read saved bundle handoff file: %v", err)
	}
	if string(data) != stub.renderBundleHandoff {
		t.Errorf("content = %q, want %q", string(data), stub.renderBundleHandoff)
	}
}

func TestRemote_ExportBundle(t *testing.T) {
	transport := newFakeArtifactTransport()
	stub := &stubRemoteBackend{
		transport:          transport,
		exportArtifactData: []byte("PK\x03\x04fake-zip-bundle-bytes"),
	}
	a := setupRemoteApp(stub)
	a.SetArtifactTransportOverride(transport)

	tmpDir := t.TempDir()
	localDest := filepath.Join(tmpDir, "exported.agent-session.zip")
	var offered string
	a.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		offered = defaultName
		return localDest, nil
	}

	ref := model.SessionRef{Agent: model.AgentCodex, ID: "6f1c2d3e-4a5b-4c6d-8e7f-001122334455"}
	gotPath, err := a.ExportBundle(engine.ExportRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if gotPath != localDest {
		t.Errorf("ExportBundle returned %q, want %q", gotPath, localDest)
	}
	// The name comes from the remote host's metadata for the session.
	if want := "codex_remote-project_6f1c2d3e.agent-session.zip"; offered != want {
		t.Errorf("default file name = %q, want %q", offered, want)
	}
	if !stub.exportCalled {
		t.Error("expected backend.ExportBundle to be called")
	}
	if !strings.HasPrefix(stub.exportDestPath, "/remote/cache/staging/export-") {
		t.Errorf("backend destPath = %q, want /remote/cache/staging/export-*", stub.exportDestPath)
	}

	data, err := os.ReadFile(localDest)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if !bytes.Equal(data, stub.exportArtifactData) {
		t.Errorf("content = %q, want %q", string(data), string(stub.exportArtifactData))
	}
	info, _ := os.Stat(localDest)
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

func TestRemote_OpenBundle(t *testing.T) {
	transport := newFakeArtifactTransport()
	stub := &stubRemoteBackend{
		transport: transport,
		openBundleSummary: engine.BundleSummary{
			BundleID: "bndl-999",
			Format:   "agent-session",
		},
	}
	a := setupRemoteApp(stub)
	a.SetArtifactTransportOverride(transport)

	tmpDir := t.TempDir()
	localBundle := filepath.Join(tmpDir, "local.agent-session.zip")
	payload := []byte("fake-bundle-payload-for-open")
	if err := os.WriteFile(localBundle, payload, 0644); err != nil {
		t.Fatalf("write local bundle: %v", err)
	}

	summary, err := a.OpenBundlePath(localBundle)
	if err != nil {
		t.Fatalf("OpenBundlePath: %v", err)
	}
	if summary.BundleID != "bndl-999" {
		t.Errorf("BundleID = %q, want 'bndl-999'", summary.BundleID)
	}
	// Path should be preserved as local path for frontend display
	if summary.Path != localBundle {
		t.Errorf("summary.Path = %q, want local %q", summary.Path, localBundle)
	}
	if !stub.openBundleCalled {
		t.Error("expected backend.OpenBundle to be called")
	}
	if !strings.HasPrefix(stub.openBundlePath, "/remote/cache/staging/import-") {
		t.Errorf("backend open path = %q, want /remote/cache/staging/import-*", stub.openBundlePath)
	}

	// Verify file was uploaded into transport
	token := strings.TrimPrefix(stub.openBundlePath, "/remote/cache/staging/")
	uploaded, ok := transport.artifacts[token]
	if !ok {
		t.Fatalf("artifact not found in transport under token %q", token)
	}
	if !bytes.Equal(uploaded, payload) {
		t.Errorf("uploaded = %q, want %q", string(uploaded), string(payload))
	}
}
