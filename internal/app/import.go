package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/remote"
)

// BundleSummary aliases engine.BundleSummary.
type BundleSummary = engine.BundleSummary

// BundleSessionSummary aliases engine.BundleSessionSummary.
type BundleSessionSummary = engine.BundleSessionSummary

// BundleHandoffRequest aliases engine.BundleHandoffRequest.
type BundleHandoffRequest = engine.BundleHandoffRequest

// OpenBundle opens a native file dialog to choose a session bundle and returns its summary.
func (a *App) OpenBundle() (BundleSummary, error) {
	ctx := a.appCtx()
	path, err := wruntime.OpenFileDialog(ctx, wruntime.OpenDialogOptions{
		Title:            "Open Session Bundle",
		DefaultDirectory: dialogDefaultDir(),
		Filters: []wruntime.FileFilter{
			{DisplayName: "Agent Session Bundles (*.agent-session.zip, *.zip)", Pattern: "*.agent-session.zip;*.zip"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return BundleSummary{}, fmt.Errorf("open bundle dialog: %w", err)
	}
	if path == "" {
		return BundleSummary{}, nil // User cancelled
	}
	return a.OpenBundlePath(path)
}

// OpenBundlePath opens a specific bundle path without showing a dialog.
func (a *App) OpenBundlePath(path string) (BundleSummary, error) {
	ctx := a.appCtx()
	if a.isRemote() {
		return a.openRemoteBundle(ctx, path)
	}
	return a.activeBackend().OpenBundle(ctx, path)
}

func (a *App) openRemoteBundle(ctx context.Context, localPath string) (BundleSummary, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return BundleSummary{}, fmt.Errorf("open bundle file: %w", err)
	}
	defer f.Close()

	token, err := remote.RandomArtifactToken("import")
	if err != nil {
		return BundleSummary{}, fmt.Errorf("artifact token: %w", err)
	}

	transport := a.artifactTransport()
	if transport == nil {
		return BundleSummary{}, errors.New("artifact transport unavailable")
	}

	if _, err := transport.PutArtifact(ctx, token, f); err != nil {
		return BundleSummary{}, fmt.Errorf("upload bundle: %w", err)
	}

	remotePath := transport.StagingPath(token)
	summary, err := a.activeBackend().OpenBundle(ctx, remotePath)
	if err != nil {
		return BundleSummary{}, err
	}
	summary.Path = localPath
	return summary, nil
}

// BuildBundleHandoff generates a handoff preview from an opened bundle.
func (a *App) BuildBundleHandoff(req BundleHandoffRequest) (HandoffPreview, error) {
	preview, err := a.activeBackend().BuildBundleHandoff(a.appCtx(), req)
	if err != nil {
		return preview, err
	}
	if host := a.remoteHost(); host != "" {
		preview.Command = remote.WrapSSHCommand(host, preview.Command)
	}
	return preview, nil
}

// BundleHandoffCommand writes the handoff files and returns the launch command.
func (a *App) BundleHandoffCommand(req BundleHandoffRequest) (string, error) {
	cmd, err := a.activeBackend().BundleHandoffCommand(a.appCtx(), req)
	if err != nil {
		return "", err
	}
	if host := a.remoteHost(); host != "" {
		cmd = remote.WrapSSHCommand(host, cmd)
	}
	return cmd, nil
}

// SaveBundleHandoff writes the full handoff document to a file chosen by the user.
func (a *App) SaveBundleHandoff(req BundleHandoffRequest) (string, error) {
	ctx := a.appCtx()
	filename := fmt.Sprintf("handoff-bundle-%s.md", req.Target)

	var dest string
	var err error
	if a.saveDialogOverride != nil {
		dest, err = a.saveDialogOverride(ctx, filename)
	} else {
		dest, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
			Title:            "Save Handoff Markdown",
			DefaultDirectory: dialogDefaultDir(),
			DefaultFilename:  filename,
			Filters: []wruntime.FileFilter{
				{DisplayName: "Markdown (*.md)", Pattern: "*.md"},
			},
		})
	}
	if err != nil {
		return "", fmt.Errorf("save bundle handoff dialog: %w", err)
	}
	if dest == "" {
		return "", nil // User cancelled
	}

	if a.isRemote() {
		md, err := a.activeBackend().RenderBundleHandoff(ctx, req)
		if err != nil {
			return "", err
		}
		return writeSecureLocalFile(dest, md)
	}

	return a.activeBackend().SaveBundleHandoff(ctx, req, dest)
}

