package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxImageBytes caps a saved image; a large diagram at 2x is a few MB.
const maxImageBytes = 64 << 20

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// SaveImageRequest is an image drawn in the app, such as a diagram, to save
// on this computer whichever host is selected. Data is the SVG text, or the
// PNG in base64.
type SaveImageRequest struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Data   string `json:"data"`
}

// SaveImage asks where to save an image and writes it there. If the dialog
// is cancelled, it returns an empty path.
func (a *App) SaveImage(req SaveImageRequest) (string, error) {
	content, err := imageContent(req)
	if err != nil {
		return "", err
	}
	name := imageFileName(req.Name, req.Format)

	ctx := a.appCtx()
	var dest string
	if a.saveDialogOverride != nil {
		dest, err = a.saveDialogOverride(ctx, name)
	} else {
		label := strings.ToUpper(req.Format)
		dest, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
			Title:            "Save Image",
			DefaultDirectory: dialogDefaultDir(),
			DefaultFilename:  name,
			Filters: fileFilters(
				wruntime.FileFilter{DisplayName: fmt.Sprintf("%s Images (*.%s)", label, req.Format), Pattern: "*." + req.Format},
			),
		})
	}
	if err != nil {
		return "", fmt.Errorf("save image: save file dialog: %w", err)
	}
	return writeSecureLocalBytes(dest, content)
}

func imageContent(req SaveImageRequest) ([]byte, error) {
	switch req.Format {
	case "svg":
		if len(req.Data) > maxImageBytes {
			return nil, fmt.Errorf("save image: the image is larger than %d MB", maxImageBytes>>20)
		}
		if !strings.Contains(req.Data, "<svg") {
			return nil, fmt.Errorf("save image: not an SVG image")
		}
		return []byte(req.Data), nil
	case "png":
		if base64.StdEncoding.DecodedLen(len(req.Data)) > maxImageBytes {
			return nil, fmt.Errorf("save image: the image is larger than %d MB", maxImageBytes>>20)
		}
		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			return nil, fmt.Errorf("save image: decode PNG: %w", err)
		}
		if !bytes.HasPrefix(data, pngSignature) {
			return nil, fmt.Errorf("save image: not a PNG image")
		}
		return data, nil
	default:
		return nil, fmt.Errorf("save image: unsupported format %q", req.Format)
	}
}

// imageFileName is the suggested file name: name without path separators or
// characters Windows refuses, "diagram" when nothing is left.
func imageFileName(name, format string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, name)
	clean = strings.Trim(clean, " .")
	if clean == "" {
		clean = "diagram"
	}
	return clean + "." + format
}
