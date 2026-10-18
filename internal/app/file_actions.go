package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/remote"
	"github.com/ginkcode/agent-sessions/internal/rpc"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ResolveTerminalFile resolves a clicked terminal path on the selected host.
// Resolution and catalog authorization deliberately happen on the backend, so
// remote paths are never interpreted using the desktop host's path semantics.
func (a *App) ResolveTerminalFile(dir, path string) (files.TerminalFile, error) {
	return a.route().backend.ResolveTerminalFile(a.appCtx(), dir, path)
}

// RevealFile selects a local project file or folder in the native file manager.
func (a *App) RevealFile(dir, path string) error {
	r := a.route()
	if r.host != "" {
		return errors.New("reveal file is only supported on the local host")
	}
	full, err := r.backend.ResolveFile(a.appCtx(), dir, path)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(a.appCtx(), "open", "-R", full).Run()
	case "windows":
		return startDetached(exec.Command("explorer.exe", "/select,"+full))
	default:
		uri := (&url.URL{Scheme: "file", Path: full}).String()
		cmd := exec.CommandContext(a.appCtx(), "dbus-send", "--session", "--dest=org.freedesktop.FileManager1", "--type=method_call", "/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems", "array:string:"+uri, "string:")
		if err := cmd.Run(); err == nil {
			return nil
		}
		return startDetached(exec.Command("xdg-open", filepath.Dir(full)))
	}
}

// CopyFile places a native file/folder reference on the clipboard, not text.
func (a *App) CopyFile(dir, path string) error {
	r := a.route()
	if r.host != "" {
		return errors.New("copy file is only supported on the local host")
	}
	full, err := r.backend.ResolveFile(a.appCtx(), dir, path)
	if err != nil {
		return err
	}
	return copyNativeFile(a.appCtx(), runtime.GOOS, full)
}

func copyNativeFile(ctx context.Context, goos, full string) error {
	switch goos {
	case "darwin":
		// argv keeps names out of AppleScript source, including quotes/newlines.
		return exec.CommandContext(ctx, "osascript", "-e", "on run argv\nset the clipboard to (POSIX file (item 1 of argv))\nend run", full).Run()
	case "windows":
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", `Set-Clipboard -LiteralPath $env:AGENT_SESSIONS_COPY_FILE`)
		cmd.Env = append(os.Environ(), "AGENT_SESSIONS_COPY_FILE="+full)
		return cmd.Run()
	default:
		payload := (&url.URL{Scheme: "file", Path: full}).String() + "\r\n"
		var cmd *exec.Cmd
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if exe, err := exec.LookPath("wl-copy"); err == nil {
				cmd = exec.CommandContext(ctx, exe, "--type", "text/uri-list")
			}
		}
		if cmd == nil {
			if exe, err := exec.LookPath("xclip"); err == nil {
				cmd = exec.CommandContext(ctx, exe, "-selection", "clipboard", "-t", "text/uri-list", "-i")
			}
		}
		if cmd == nil {
			return errors.New("native file clipboard requires wl-copy (Wayland) or xclip (X11)")
		}
		cmd.Stdin = strings.NewReader(payload)
		return cmd.Run()
	}
}

// DownloadFile saves an original remote file, or a directory ZIP. Cancellation
// returns an empty path. File data streams over the artifact channel, not JSON.
func (a *App) DownloadFile(dir, path string) (string, error) {
	ctx := a.appCtx()
	r := a.route()
	if r.host == "" {
		return "", errors.New("download file requires a remote host")
	}
	if r.transport == nil {
		return "", fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
	}
	token, err := remote.RandomArtifactToken("download")
	if err != nil {
		return "", err
	}
	// Prepare first so server-authorized metadata determines the suggested name.
	artifact, err := r.backend.PrepareFileDownload(ctx, dir, path, token)
	if err != nil {
		return "", err
	}
	// A cancelled dialog still consumes/removes the staged artifact.
	var dest string
	if a.saveDialogOverride != nil {
		dest, err = a.saveDialogOverride(ctx, artifact.Name)
	} else {
		dest, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{Title: "Download File", DefaultDirectory: dialogDefaultDir(), DefaultFilename: artifact.Name})
	}
	if err != nil || dest == "" {
		_ = r.transport.GetArtifact(ctx, token, true, discardWriter{})
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*.tmp")
	if err != nil {
		_ = r.transport.GetArtifact(ctx, token, true, discardWriter{})
		return "", err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if err = r.transport.GetArtifact(ctx, token, true, tmp); err != nil {
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
