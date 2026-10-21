//go:build linux || darwin || windows

package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func renameFixture(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRenameFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	renameFixture(t, root, "sub/old.bin")
	ctx := context.Background()
	if err := Rename(ctx, EditPolicy{}, root, "sub/old.bin", "new.bin"); err != nil {
		t.Fatal(err)
	}
	if err := Rename(ctx, EditPolicy{}, root, "sub", "folder"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "folder/new.bin"))
	if err != nil || string(data) != "original" {
		t.Fatalf("content %q: %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
		t.Fatal("source remains")
	}
}

func TestRenameRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	renameFixture(t, root, "sub/file")
	renameFixture(t, root, "sub/existing")
	renameFixture(t, root, ".git/config")
	if err := os.Symlink("sub", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, name string }{
		{"", "new"}, {".", "new"}, {"..", "new"}, {"sub/../sub/file", "new"},
		{filepath.Join(root, "sub/file"), "new"}, {"sub/file", "../new"}, {"sub/file", "a/b"},
		{"sub/file", "a\\b"}, {"sub/file", ""}, {"sub/file", "."}, {"sub/file", ".."},
		{"sub/file", "auth.json"}, {".git", "repo"}, {"link/file", "new"}, {"link", "new"},
		{"sub/file", "existing"}, {"sub/file", "file"}, {filepath.Join("sub", "file"), "file"}, {"sub/missing", "new"},
	} {
		t.Run(tc.path+"->"+tc.name, func(t *testing.T) {
			if err := Rename(context.Background(), EditPolicy{}, root, tc.path, tc.name); err == nil {
				t.Fatal("unsafe rename accepted")
			}
		})
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sub/file")); string(b) != "original" {
		t.Fatal("source modified")
	}
}

func TestRenameProtectsNestedStoresAndMetadata(t *testing.T) {
	for _, nested := range []string{"store/data", ".git/config", "auth.json", "deep/.ssh/key"} {
		t.Run(nested, func(t *testing.T) {
			root := t.TempDir()
			renameFixture(t, root, "folder/"+nested)
			policy := EditPolicy{}
			if nested == "store/data" {
				policy.ProtectedRoots = []string{filepath.Join(root, "folder/store")}
			}
			err := Rename(context.Background(), policy, root, "folder", "renamed")
			if !errors.Is(err, ErrEditProtected) {
				t.Fatalf("got %v", err)
			}
		})
	}
	// Even nonexistent configured stores inside a moved directory are protected.
	root := t.TempDir()
	renameFixture(t, root, "folder/file")
	policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "folder/future/store")}}
	if err := Rename(context.Background(), policy, root, "folder", "new"); !errors.Is(err, ErrEditProtected) {
		t.Fatal(err)
	}
}

func TestRenameNoReplacePrimitive(t *testing.T) {
	root := t.TempDir()
	renameFixture(t, root, "source")
	renameFixture(t, root, "destination")
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := renameNoReplace(dir, "source", "destination"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v", err)
	}
	// Multiple competing sources targeting one leaf have exactly one winner.
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := range 20 {
		name := string(rune('a' + i))
		renameFixture(t, root, name)
		wg.Go(func() { results <- renameNoReplace(dir, name, "winner") })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d winners", winners)
	}
}

func TestRenameSerializesWithSaves(t *testing.T) {
	root := t.TempDir()
	renameFixture(t, root, "file")
	unlock, err := lockSaves(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = Rename(ctx, EditPolicy{}, root, "file", "new")
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if err := Rename(context.Background(), EditPolicy{}, root, "file", "new"); err != nil {
		t.Fatal(err)
	}
}
