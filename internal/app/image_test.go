package app

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveImage(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	dir := t.TempDir()
	var asked string
	a.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		asked = defaultName
		return filepath.Join(dir, defaultName), nil
	}

	svg := `<svg xmlns="http://www.w3.org/2000/svg"></svg>`
	path, err := a.SaveImage(SaveImageRequest{Name: "flow", Format: "svg", Data: svg})
	if err != nil {
		t.Fatalf("SaveImage svg: %v", err)
	}
	if asked != "flow.svg" || path != filepath.Join(dir, "flow.svg") {
		t.Fatalf("asked %q, saved %q", asked, path)
	}
	if got, _ := os.ReadFile(path); string(got) != svg {
		t.Fatalf("svg content = %q", got)
	}

	png := append([]byte("\x89PNG\r\n\x1a\n"), 0, 1, 2, 0xff)
	path, err = a.SaveImage(SaveImageRequest{Format: "png", Data: base64.StdEncoding.EncodeToString(png)})
	if err != nil {
		t.Fatalf("SaveImage png: %v", err)
	}
	if asked != "diagram.png" {
		t.Fatalf("default name = %q", asked)
	}
	if got, _ := os.ReadFile(path); string(got) != string(png) {
		t.Fatalf("png content = %v", got)
	}
}

func TestSaveImageCancelled(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	a.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		return "", nil
	}
	path, err := a.SaveImage(SaveImageRequest{Format: "svg", Data: "<svg></svg>"})
	if err != nil || path != "" {
		t.Fatalf("cancelled = %q, %v", path, err)
	}
}

func TestSaveImageRejectsBadData(t *testing.T) {
	a, _, _ := setupHandoffTest(t)
	a.saveDialogOverride = func(ctx context.Context, defaultName string) (string, error) {
		t.Fatalf("dialog opened for bad data")
		return "", nil
	}
	for _, req := range []SaveImageRequest{
		{Format: "gif", Data: "x"},
		{Format: "svg", Data: "<html></html>"},
		{Format: "png", Data: "not base64!"},
		{Format: "png", Data: base64.StdEncoding.EncodeToString([]byte("GIF89a"))},
	} {
		if _, err := a.SaveImage(req); err == nil {
			t.Errorf("SaveImage(%s, %q) succeeded", req.Format, req.Data)
		}
	}
}

func TestImageFileName(t *testing.T) {
	for name, want := range map[string]string{
		"":              "diagram.svg",
		"flow chart":    "flow chart.svg",
		`a/b\c:d*?"<>|`: "abcd.svg",
		" .. ":          "diagram.svg",
	} {
		if got := imageFileName(name, "svg"); got != want {
			t.Errorf("imageFileName(%q) = %q, want %q", name, got, want)
		}
	}
	if strings.ContainsAny(imageFileName("x\ny", "png"), "\n") {
		t.Error("control characters kept")
	}
}
