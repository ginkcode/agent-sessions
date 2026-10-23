package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestCreateDirectoryAuthorization(t *testing.T) {
	root := t.TempDir()
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{CWD: root}})
	e := &Engine{svc: NewService(catalog, nil), roots: paths.Roots{Cache: filepath.Join(root, "cache")}}
	ctx := context.Background()
	if err := e.CreateDirectory(ctx, t.TempDir(), "", "new"); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("unknown root: %v", err)
	}
	if err := e.CreateDirectory(ctx, root, "", "cache"); !errors.Is(err, files.ErrEditProtected) {
		t.Fatalf("protected store: %v", err)
	}
	if err := e.CreateDirectory(ctx, root, "", "new"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateDirectory(ctx, root, "new", "nested"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateFile(ctx, t.TempDir(), "", "new.go"); !errors.Is(err, ErrUnknownDirectory) {
		t.Fatalf("unknown root: %v", err)
	}
	if err := e.CreateFile(ctx, root, "", "cache"); !errors.Is(err, files.ErrEditProtected) {
		t.Fatalf("protected store: %v", err)
	}
	if err := e.CreateFile(ctx, root, "new", "file.go"); err != nil {
		t.Fatal(err)
	}
}
