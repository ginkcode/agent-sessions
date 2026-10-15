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

// imageFormat is a raster format SaveImage takes, known by its first bytes.
type imageFormat struct {
	ext   string
	label string
	magic func([]byte) bool
}

var imageFormats = map[string]imageFormat{
	"png":  {"png", "PNG", func(b []byte) bool { return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) }},
	"jpeg": {"jpg", "JPEG", func(b []byte) bool { return bytes.HasPrefix(b, []byte("\xff\xd8\xff")) }},
	"gif": {"gif", "GIF", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a"))
	}},
	"webp": {"webp", "WebP", func(b []byte) bool {
		return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}},
}

// SaveImageRequest is an image to save on this computer whichever host is
// selected: a diagram drawn in the app, or an image attached to a session.
// Format is svg, png, jpeg, gif or webp. Data is the SVG text, or the
// image's bytes in base64.
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
	ext, label := "svg", "SVG"
	if f, ok := imageFormats[req.Format]; ok {
		ext, label = f.ext, f.label
	}
	name := imageFileName(req.Name, ext)

	ctx := a.appCtx()
	var dest string
	if a.saveDialogOverride != nil {
		dest, err = a.saveDialogOverride(ctx, name)
	} else {
		dest, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
			Title:            "Save Image",
			DefaultDirectory: dialogDefaultDir(),
			DefaultFilename:  name,
			Filters: fileFilters(
				wruntime.FileFilter{DisplayName: fmt.Sprintf("%s Images (*.%s)", label, ext), Pattern: "*." + ext},
			),
		})
	}
	if err != nil {
		return "", fmt.Errorf("save image: save file dialog: %w", err)
	}
	return writeSecureLocalBytes(dest, content)
}

func imageContent(req SaveImageRequest) ([]byte, error) {
	if req.Format == "svg" {
		if len(req.Data) > maxImageBytes {
			return nil, fmt.Errorf("save image: the image is larger than %d MB", maxImageBytes>>20)
		}
		if !strings.Contains(req.Data, "<svg") {
			return nil, fmt.Errorf("save image: not an SVG image")
		}
		return []byte(req.Data), nil
	}
	f, ok := imageFormats[req.Format]
	if !ok {
		return nil, fmt.Errorf("save image: unsupported format %q", req.Format)
	}
	if base64.StdEncoding.DecodedLen(len(req.Data)) > maxImageBytes {
		return nil, fmt.Errorf("save image: the image is larger than %d MB", maxImageBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return nil, fmt.Errorf("save image: decode %s: %w", f.label, err)
	}
	if !f.magic(data) {
		return nil, fmt.Errorf("save image: not a %s image", f.label)
	}
	return data, nil
}

// imageFileName is the suggested file name: name without path separators or
// characters Windows refuses, "diagram" when nothing is left, and a known
// image extension replaced by ext.
func imageFileName(name, ext string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, name)
	clean = strings.Trim(clean, " .")
	if dot := strings.LastIndexByte(clean, '.'); dot > 0 {
		switch strings.ToLower(clean[dot+1:]) {
		case "svg", "png", "jpg", "jpeg", "gif", "webp":
			clean = clean[:dot]
		}
	}
	if clean == "" {
		clean = "diagram"
	}
	return clean + "." + ext
}
