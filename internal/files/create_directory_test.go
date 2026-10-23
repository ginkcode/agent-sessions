package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCreateDirectory(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	for _, tc := range []struct{ path, name string }{{"", "folder"}, {"folder", "nested"}, {".", "sibling"}} {
		if err := CreateDirectory(ctx, EditPolicy{}, root, tc.path, tc.name); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(filepath.Join(root, tc.path, tc.name)); err != nil || !info.IsDir() {
			t.Fatalf("directory: %v", err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		wg.Go(func() { results <- CreateDirectory(ctx, EditPolicy{}, root, "", "winner") })
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

func TestCreateDirectoryRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "parent"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, name string }{
		{"", ""}, {"", "."}, {"", ".."}, {"", "a/b"}, {"", "a\\b"}, {"", "a\x00b"},
		{"..", "bad"}, {"parent/../parent", "bad"}, {root, "bad"}, {"missing", "bad"}, {"file", "bad"},
		{"", "file"}, {"", "parent"}, {"", ".git"}, {"", "auth.json"}, {"", ".credentials-new"},
	} {
		if err := CreateDirectory(context.Background(), EditPolicy{}, root, tc.path, tc.name); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if runtime.GOOS == "windows" {
		for _, tc := range []struct{ path, name string }{{"", "GIT~1"}, {"parent~1", "bad"}, {"", "file:stream"}, {"", "folder."}, {"parent ", "bad"}} {
			if err := CreateDirectory(context.Background(), EditPolicy{}, root, tc.path, tc.name); err == nil {
				t.Fatalf("accepted alias %+v", tc)
			}
		}
	}
	policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "future/store")}}
	if err := CreateDirectory(context.Background(), policy, root, "", "future"); !errors.Is(err, ErrEditProtected) {
		t.Fatal(err)
	}
	if err := os.Symlink("parent", filepath.Join(root, "link")); err == nil {
		if err := CreateDirectory(context.Background(), EditPolicy{}, root, "link", "bad"); !errors.Is(err, ErrEditLink) {
			t.Fatal(err)
		}
		if err := CreateDirectory(context.Background(), EditPolicy{}, root, "", "link"); !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
}

func TestCreateDirectorySerializesWithSaves(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockSaves(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = CreateDirectory(ctx, EditPolicy{}, root, "", "new")
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !os.IsNotExist(err) {
		t.Fatal("created despite cancellation")
	}
}

func TestCreateFile(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, name string }{{"", "main.go"}, {"folder", "nested.ts"}} {
		if err := CreateFile(ctx, EditPolicy{}, root, tc.path, tc.name); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Lstat(filepath.Join(root, tc.path, tc.name)); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			t.Fatalf("file: %v %v", info, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "kept"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, name string }{
		{"", ""}, {"", ".."}, {"", "a/b"}, {"..", "bad"}, {"missing", "bad"}, {"kept", "bad"},
		{"", "kept"}, {"", "folder"}, {"", ".git"}, {"", "auth.json"},
	} {
		if err := CreateFile(ctx, EditPolicy{}, root, tc.path, tc.name); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "kept")); err != nil || string(data) != "original" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
	if err := os.Symlink("folder", filepath.Join(root, "link")); err == nil {
		if err := CreateFile(ctx, EditPolicy{}, root, "link", "bad"); !errors.Is(err, ErrEditLink) {
			t.Fatal(err)
		}
		if err := CreateFile(ctx, EditPolicy{}, root, "", "link"); !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
}
