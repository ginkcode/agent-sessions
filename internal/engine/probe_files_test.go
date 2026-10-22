package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestProbeFilesCatalogFirst(t *testing.T) {
	root := t.TempDir()
	e := editEngine(root)
	// Even invalid batches must first be rejected for an unauthorized directory.
	for _, dir := range []string{"", t.TempDir(), filepath.Dir(root), filepath.Join(root, "child")} {
		if _, err := e.ProbeFiles(context.Background(), dir, nil); !errors.Is(err, ErrUnknownDirectory) {
			t.Fatalf("%q: %v", dir, err)
		}
	}
	out, err := e.ProbeFiles(context.Background(), root, []string{"missing"})
	if err != nil || out.Observations[0].State != "absent" {
		t.Fatalf("%+v %v", out, err)
	}
}
