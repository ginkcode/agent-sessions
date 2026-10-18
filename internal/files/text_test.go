package files

import (
	"context"
	"testing"
)

func TestReadTextWithoutKnownExtension(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{".husky/pre-commit", "deploy/app.service", "config.custom", "LICENSE"} {
		t.Run(path, func(t *testing.T) {
			text := "Text configuration\n第二行\n"
			write(t, root, path, text)
			got, err := Read(context.Background(), root, path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Binary || got.Text != text {
				t.Fatalf("expected text content, got %+v", got)
			}
		})
	}
	write(t, root, "binary.custom", "text\x00binary")
	got, err := Read(context.Background(), root, "binary.custom")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Binary || got.Text != "" {
		t.Fatalf("expected binary content to be excluded, got %+v", got)
	}
}
