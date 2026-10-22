package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func deleteFixture(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	deleteFixture(t, root, "folder/file.bin")
	deleteFixture(t, root, "folder/deep/other")
	deleteFixture(t, root, "sibling")
	for _, rel := range []string{"folder/file.bin", "folder"} {
		if err := Delete(context.Background(), EditPolicy{}, root, rel); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s remains: %v", rel, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "sibling")); err != nil || string(data) != "keep" {
		t.Fatalf("sibling modified: %q %v", data, err)
	}
	if err := Delete(context.Background(), EditPolicy{}, root, "folder"); err == nil {
		t.Fatal("missing target accepted")
	}
}

func TestDeleteRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	deleteFixture(t, root, "sub/file")
	deleteFixture(t, root, ".git/config")
	deleteFixture(t, root, "auth.json")
	for _, rel := range []string{"", ".", "./", "..", "sub/..", "sub/../sub/file", "../outside", filepath.Join(root, "sub/file"), ".git", ".git/config", "auth.json"} {
		t.Run(rel, func(t *testing.T) {
			if err := Delete(context.Background(), EditPolicy{}, root, rel); err == nil {
				t.Fatal("unsafe delete accepted")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "sub/file")); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteProtectsNestedStoresAndMetadata(t *testing.T) {
	for _, nested := range []string{"store/data", ".git/config", "AUTH.JSON", "deep/.ssh/key", "opencode.db-wal", "state_1.sqlite"} {
		t.Run(nested, func(t *testing.T) {
			root := t.TempDir()
			deleteFixture(t, root, "folder/"+nested)
			deleteFixture(t, root, "folder/ordinary")
			policy := EditPolicy{}
			if nested == "store/data" {
				policy.ProtectedRoots = []string{filepath.Join(root, "folder/store")}
			}
			if err := Delete(context.Background(), policy, root, "folder"); !errors.Is(err, ErrEditProtected) {
				t.Fatalf("got %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "folder/ordinary")); err != nil {
				t.Fatalf("partial delete: %v", err)
			}
		})
	}
	root := t.TempDir()
	deleteFixture(t, root, "folder/file")
	policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "folder/future/store")}}
	if err := Delete(context.Background(), policy, root, "folder"); !errors.Is(err, ErrEditProtected) {
		t.Fatal(err)
	}
}

func TestDeleteRejectsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	deleteFixture(t, outside, "file")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	deleteFixture(t, root, "folder/file")
	if err := os.Symlink(outside, filepath.Join(root, "folder/link")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"link", "link/file", "folder"} {
		if err := Delete(context.Background(), EditPolicy{}, root, rel); !errors.Is(err, ErrEditLink) {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	for _, path := range []string{filepath.Join(outside, "file"), filepath.Join(root, "folder/file")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteSerializesWithSaves(t *testing.T) {
	root := t.TempDir()
	deleteFixture(t, root, "file")
	unlock, err := lockSaves(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = Delete(ctx, EditPolicy{}, root, "file")
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file")); err != nil {
		t.Fatal(err)
	}
	if err := Delete(context.Background(), EditPolicy{}, root, "file"); err != nil {
		t.Fatal(err)
	}
}
