package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// ExportRequest aliases engine.ExportRequest.
type ExportRequest = engine.ExportRequest

// ExportPreview aliases engine.ExportPreview.
type ExportPreview = engine.ExportPreview

// exportFileName names the bundle after the session's agent, directory and
// ID. When the session's metadata is unavailable (for example, the host just
// dropped), the name falls back to the agent and ID from the ref.
func (a *App) exportFileName(ctx context.Context, ref model.SessionRef) string {
	meta, err := a.activeBackend().GetSessionMeta(ctx, ref)
	if err != nil {
		meta = model.SessionMeta{Ref: ref}
	}
	return engine.ExportFileName(meta)
}

// PreviewExport estimates an export for the desktop frontend.
func (a *App) PreviewExport(req ExportRequest) (ExportPreview, error) {
	return a.activeBackend().PreviewExport(a.appCtx(), req)
}

// ExportBundle opens a save dialog and writes the bundle. A cancelled dialog
// returns an empty path.
func (a *App) ExportBundle(req ExportRequest) (string, error) {
	ctx := a.appCtx()
	name := a.exportFileName(ctx, req.Ref)
	var dest string
	var err error
	if a.saveDialogOverride != nil {
		dest, err = a.saveDialogOverride(ctx, name)
	} else {
		dest, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
			Title:            "Export Session",
			DefaultDirectory: dialogDefaultDir(),
			DefaultFilename:  name,
			Filters: fileFilters(
				wruntime.FileFilter{DisplayName: "Agent Session Bundles (*.agent-session.zip)", Pattern: "*.agent-session.zip"},
			),
		})
	}
	if err != nil {
		return "", fmt.Errorf("export: save file dialog: %w", err)
	}
	if dest == "" {
		return "", nil
	}

	r := a.route()
	if r.host != "" {
		return a.exportRemoteBundle(ctx, r, req, dest)
	}

	return r.backend.ExportBundle(ctx, req, dest)
}

func (a *App) exportRemoteBundle(ctx context.Context, r route, req ExportRequest, localDest string) (string, error) {
	token, err := remote.RandomArtifactToken("export")
	if err != nil {
		return "", fmt.Errorf("artifact token: %w", err)
	}

	transport := r.transport
	if transport == nil {
		return "", fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
	}

	remoteStagingPath := transport.StagingPath(token)
	if _, err := r.backend.ExportBundle(ctx, req, remoteStagingPath); err != nil {
		return "", err
	}

	dir := filepath.Dir(localDest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("export mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".export-*.tmp")
	if err != nil {
		return "", fmt.Errorf("export create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("export chmod %s: %w", tmpName, err)
	}

	if err := transport.GetArtifact(ctx, token, true, tmp); err != nil {
		return "", fmt.Errorf("download export bundle: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("export sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("export close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, localDest); err != nil {
		return "", fmt.Errorf("export rename %s -> %s: %w", tmpName, localDest, err)
	}

	return localDest, nil
}

// dialogDefaultDir is the folder the export and import file dialogs open in.
func dialogDefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
