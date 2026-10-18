package files

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDownload(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "folder", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte{0, 1, 2, 255}
	if err := os.WriteFile(filepath.Join(root, "folder", "binary"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	meta, err := WriteDownload(context.Background(), root, "folder/binary", &b)
	if err != nil || meta.Name != "binary" || meta.Directory || !bytes.Equal(b.Bytes(), data) {
		t.Fatalf("file: %+v %v %v", meta, err, b.Bytes())
	}
	for _, path := range []string{"folder", "."} {
		b.Reset()
		meta, err = WriteDownload(context.Background(), root, path, &b)
		if err != nil || !meta.Directory {
			t.Fatalf("directory: %+v %v", meta, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range zr.File {
			if filepath.Base(f.Name) == "binary" {
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("archive bytes: %v %v", got, err)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("missing binary")
		}
	}
}

func TestWriteDownloadRejectsEscapesAndRecursiveSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	for _, path := range []string{"../secret", filepath.Join(outside, "secret"), "escape/secret", "."} {
		var b bytes.Buffer
		if _, err := WriteDownload(context.Background(), root, path, &b); err == nil {
			t.Errorf("accepted %q", path)
		}
		if bytes.Contains(b.Bytes(), []byte("secret")) {
			t.Errorf("leaked secret for %q", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WriteDownload(ctx, root, ".", io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
