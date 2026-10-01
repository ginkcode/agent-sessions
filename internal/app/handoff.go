package app

import (
	"fmt"
	"os"
	"path/filepath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// HandoffRequest aliases engine.HandoffRequest.
type HandoffRequest = engine.HandoffRequest

// HandoffPreview aliases engine.HandoffPreview.
type HandoffPreview = engine.HandoffPreview

// HandoffCacheInfo aliases engine.HandoffCacheInfo.
type HandoffCacheInfo = engine.HandoffCacheInfo

// BuildHandoff renders the handoff document preview for the desktop frontend.
func (a *App) BuildHandoff(req HandoffRequest) (HandoffPreview, error) {
	r := a.route()
	preview, err := r.backend.BuildHandoff(a.appCtx(), req)
	if err != nil {
		return preview, err
	}
	preview.Command = r.wrap(preview.Command)
	return preview, nil
}

// HandoffCommand writes the handoff files and returns the launch command.
func (a *App) HandoffCommand(req HandoffRequest) (string, error) {
	r := a.route()
	cmd, err := r.backend.HandoffCommand(a.appCtx(), req)
	if err != nil {
		return "", err
	}
	return r.wrap(cmd), nil
}

// SaveHandoff opens a file save dialog and saves the self-contained full handoff
// document to the chosen file. If the dialog is cancelled, it returns an empty path.
func (a *App) SaveHandoff(req HandoffRequest) (string, error) {
	ctx := a.appCtx()
	defaultName := "handoff.md"
	if req.Ref.ID != "" {
		defaultName = req.Ref.ID + "-handoff.md"
	}

	var destPath string
	var err error
	if a.saveDialogOverride != nil {
		destPath, err = a.saveDialogOverride(ctx, defaultName)
	} else {
		destPath, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
			Title:            "Save Handoff Document",
			DefaultDirectory: dialogDefaultDir(),
			DefaultFilename:  defaultName,
			Filters: fileFilters(
				wruntime.FileFilter{DisplayName: "Markdown Files (*.md)", Pattern: "*.md"},
				wruntime.FileFilter{DisplayName: "All Files (*.*)", Pattern: "*.*"},
			),
		})
	}
	if err != nil {
		return "", fmt.Errorf("handoff: save file dialog: %w", err)
	}
	if destPath == "" {
		return "", nil
	}

	r := a.route()
	if r.host != "" {
		md, err := r.backend.RenderHandoff(ctx, req)
		if err != nil {
			return "", err
		}
		return writeSecureLocalFile(destPath, md)
	}

	return r.backend.SaveHandoff(ctx, req, destPath)
}

func writeSecureLocalFile(destPath, content string) (string, error) {
	if destPath == "" {
		return "", nil
	}
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("save file mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".save-*.tmp")
	if err != nil {
		return "", fmt.Errorf("save file create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("save file chmod: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		return "", fmt.Errorf("save file write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("save file sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("save file close: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return "", fmt.Errorf("save file rename %s -> %s: %w", tmpName, destPath, err)
	}
	return destPath, nil
}

// HandoffCache reports the handoff files kept in the data directory.
func (a *App) HandoffCache() HandoffCacheInfo {
	info, _ := a.activeBackend().HandoffCache(a.appCtx())
	return info
}

// ClearHandoffCache deletes the handoff files kept in the data directory.
func (a *App) ClearHandoffCache() (HandoffCacheInfo, error) {
	return a.activeBackend().ClearHandoffCache(a.appCtx())
}
