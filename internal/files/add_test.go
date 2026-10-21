package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAddSafeTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	data := []byte{0, 255, 1, 2}
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenAddSource(source)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Mkdir(filepath.Join(root, "sub"), 0700)
	os.Mkdir(filepath.Join(root, ".git"), 0700)
	os.Mkdir(filepath.Join(root, "store"), 0700)
	os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link"))
	policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "store")}}
	for _, path := range []string{"../escape", "sub/../escape", "/absolute", ".git/config", "auth.json", "store/new", "link/new", "missing/new"} {
		if err := Add(context.Background(), policy, root, path, f); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	if err := Add(context.Background(), policy, root, "sub/new.bin", f); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "sub/new.bin"))
	if err != nil || string(got) != string(data) {
		t.Fatalf("copy %v %v", got, err)
	}
	if err := Add(context.Background(), policy, root, "sub/new.bin", f); err == nil {
		t.Fatal("overwrite accepted")
	}
	os.Symlink(source, filepath.Join(root, "leaf"))
	if err := Add(context.Background(), policy, root, "leaf", f); err == nil {
		t.Fatal("leaf link accepted")
	}
	if _, err := OpenAddSource(filepath.Join(root, "leaf")); err == nil {
		t.Fatal("source link accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Add(ctx, policy, root, "cancelled", f); err == nil {
		t.Fatal("cancel accepted")
	}
	if _, err := os.Lstat(filepath.Join(root, "cancelled")); !os.IsNotExist(err) {
		t.Fatal("cancel created file")
	}
}
