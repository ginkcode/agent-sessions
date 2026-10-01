package app

import (
	"context"
	"fmt"
	"os"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
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
		Filters: fileFilters(
			wruntime.FileFilter{DisplayName: "Agent Session Bundles (*.agent-session.zip, *.zip)", Pattern: "*.agent-session.zip;*.zip"},
			wruntime.FileFilter{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		),
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
	r := a.route()
	if r.host != "" {
		return a.openRemoteBundle(ctx, r, path)
	}
	return r.backend.OpenBundle(ctx, path)
}

func (a *App) openRemoteBundle(ctx context.Context, r route, localPath string) (BundleSummary, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return BundleSummary{}, fmt.Errorf("open bundle file: %w", err)
	}
	defer f.Close()

	token, err := remote.RandomArtifactToken("import")
	if err != nil {
		return BundleSummary{}, fmt.Errorf("artifact token: %w", err)
	}

	transport := r.transport
	if transport == nil {
		return BundleSummary{}, fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
	}

	if _, err := transport.PutArtifact(ctx, token, f); err != nil {
		return BundleSummary{}, fmt.Errorf("upload bundle: %w", err)
	}

	remotePath := transport.StagingPath(token)
	summary, err := r.backend.OpenBundle(ctx, remotePath)
	if err != nil {
		return BundleSummary{}, err
	}
	summary.Path = localPath
	return summary, nil
}

// BuildBundleHandoff generates a handoff preview from an opened bundle.
func (a *App) BuildBundleHandoff(req BundleHandoffRequest) (HandoffPreview, error) {
	return a.route().backend.BuildBundleHandoff(a.appCtx(), req)
}

// BundleHandoffCommand writes the handoff files and returns the launch command.
func (a *App) BundleHandoffCommand(req BundleHandoffRequest) (string, error) {
	return a.route().backend.BundleHandoffCommand(a.appCtx(), req)
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
			Filters: fileFilters(
				wruntime.FileFilter{DisplayName: "Markdown (*.md)", Pattern: "*.md"},
			),
		})
	}
	if err != nil {
		return "", fmt.Errorf("save bundle handoff dialog: %w", err)
	}
	if dest == "" {
		return "", nil // User cancelled
	}

	r := a.route()
	if r.host != "" {
		md, err := r.backend.RenderBundleHandoff(ctx, req)
		if err != nil {
			return "", err
		}
		return writeSecureLocalFile(dest, md)
	}

	return r.backend.SaveBundleHandoff(ctx, req, dest)
}
