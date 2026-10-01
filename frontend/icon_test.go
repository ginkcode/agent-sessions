package frontend

import (
	"bytes"
	"image/png"
	"testing"
)

func TestAppIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(AppIcon))
	if err != nil {
		t.Fatalf("decode embedded icon: %v", err)
	}
	if got := img.Bounds().Size(); got.X != 1024 || got.Y != 1024 {
		t.Fatalf("icon dimensions = %dx%d, want 1024x1024", got.X, got.Y)
	}
	// The corner must show the desktop through it, unlike the macOS tile.
	_, _, _, alpha := img.At(0, 0).RGBA()
	if alpha > 0xffff/16 {
		t.Fatalf("icon corner alpha = %#04x, want near transparent", alpha)
	}
}
