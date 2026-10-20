package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenEditTargetSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, body, newline string
		bom                 bool
	}{
		{"empty", "", NewlineNone, false},
		{"plain", "hello", NewlineNone, false},
		{"lf", "héllo\n世界\n", NewlineLF, false},
		{"crlf", "a\r\nb", NewlineCRLF, false},
		{"bom crlf", "\xef\xbb\xbfa\r\nb\r\n", NewlineCRLF, true},
		{"bom only", "\xef\xbb\xbf", NewlineNone, true},
		{"limit", strings.Repeat("x", MaxEditBytes), NewlineNone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "one/two/file.txt", tc.body)
			path := filepath.Join(root, "one", "two", "file.txt")
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			target, err := openEditTarget(context.Background(), EditPolicy{}, root, "./one/./two/file.txt")
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if string(target.data) != tc.body || target.revision != sha(tc.body) || target.name != "file.txt" {
				t.Fatalf("snapshot changed: name=%q revision=%q size=%d", target.name, target.revision, len(target.data))
			}
			want := EditInfo{Newline: tc.newline, BOM: tc.bom, Reason: EditNotChecked}
			if target.format != want {
				t.Fatalf("format = %+v, want %+v", target.format, want)
			}
			if !os.SameFile(before, target.info) {
				t.Fatal("snapshot identity changed")
			}
			parentInfo, err := target.parent.Lstat(target.name)
			if err != nil || !os.SameFile(before, parentInfo) {
				t.Fatalf("parent does not address target: %v", err)
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != tc.body {
				t.Fatalf("disk bytes changed: %v", err)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
				t.Fatalf("disk metadata changed: %v", err)
			}
		})
	}
}

func TestOpenEditTargetRejectsText(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"over limit", strings.Repeat("x", MaxEditBytes+1), ErrEditTooLarge},
		{"much larger", strings.Repeat("x", MaxEditBytes*3), ErrEditTooLarge},
		{"nul", "hello\x00world", ErrUnsupportedEditText},
		{"invalid utf8", "caf\xe9", ErrUnsupportedEditText},
		{"mixed", "a\n b\r\n", ErrUnsupportedEditText},
		{"bare cr", "a\rb", ErrUnsupportedEditText},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "nested/file", tc.body)
			target, err := openEditTarget(context.Background(), EditPolicy{}, root, "nested/file")
			if target != nil || !errors.Is(err, tc.want) {
				if target != nil {
					target.Close()
				}
				t.Fatalf("open = %v, %v; want nil, %v", target, err, tc.want)
			}
			body, err := os.ReadFile(filepath.Join(root, "nested", "file"))
			if err != nil || string(body) != tc.body {
				t.Fatalf("failure changed target bytes: %v", err)
			}
		})
	}
}

func TestOpenEditTargetPathsAndPolicy(t *testing.T) {
	root := t.TempDir()
	write(t, root, "dir/file", "text")
	write(t, root, "store/file", "text")
	write(t, root, ".ssh/key", "text")
	for _, tc := range []struct {
		rel  string
		want error
	}{
		{"missing", ErrEditTarget},
		{"dir", ErrEditTarget},
		{"dir/file/child", ErrEditTarget},
		{"dir/../dir/file", ErrOutsideRoot},
		{"../file", ErrOutsideRoot},
		{"", ErrOutsideRoot},
		{".ssh/key", ErrEditProtected},
		{"store/file", ErrEditProtected},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "store")}}
			target, err := openEditTarget(context.Background(), policy, root, tc.rel)
			if target != nil {
				target.Close()
				t.Fatal("rejected target returned handles")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("open = %v, want %v", err, tc.want)
			}
		})
	}
	editPathSymlink(t, "dir/file", filepath.Join(root, "file-link"))
	editPathSymlink(t, "dir", filepath.Join(root, "dir-link"))
	for _, rel := range []string{"file-link", "dir-link/file"} {
		if target, err := openEditTarget(context.Background(), EditPolicy{}, root, rel); target != nil || !errors.Is(err, ErrEditLink) {
			if target != nil {
				target.Close()
			}
			t.Fatalf("child symlink %q: %v", rel, err)
		}
	}
	alias := filepath.Join(t.TempDir(), "project")
	editPathSymlink(t, root, alias)
	target, err := openEditTarget(context.Background(), EditPolicy{}, alias, "dir/file")
	if err != nil {
		t.Fatalf("root symlink: %v", err)
	}
	target.Close()
}

func TestOpenEditTargetCancellation(t *testing.T) {
	root := t.TempDir()
	write(t, root, "file", "text")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if target, err := openEditTarget(ctx, EditPolicy{}, root, "file"); target != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open = %v, %v", target, err)
	}
	// Policy runs first, even when the context has already been cancelled.
	if _, err := openEditTarget(ctx, EditPolicy{}, root, ".ssh/key"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("policy must precede cancellation: %v", err)
	}
}

func TestEditTargetClose(t *testing.T) {
	root := t.TempDir()
	write(t, root, "dir/file", "text")
	target, err := openEditTarget(context.Background(), EditPolicy{}, root, "dir/file")
	if err != nil {
		t.Fatal(err)
	}
	file, parent := target.file, target.parent
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing again reports ErrClosed on every platform; Stat on a closed file
	// does not on Windows, where it fails with an invalid-handle error.
	if err := file.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("file still open: %v", err)
	}
	if _, err := parent.Lstat("file"); !errors.Is(err, os.ErrClosed) {
		t.Errorf("parent still open: %v", err)
	}
	if err := target.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := (*editTarget)(nil).Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}

	// A file-close failure must not prevent releasing the parent handle.
	target, err = openEditTarget(context.Background(), EditPolicy{}, root, "dir/file")
	if err != nil {
		t.Fatal(err)
	}
	parent = target.parent
	if err := target.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("expected file close failure, got %v", err)
	}
	if _, err := parent.Lstat("file"); !errors.Is(err, os.ErrClosed) {
		t.Errorf("parent leaked after file-close failure: %v", err)
	}
}

func TestEditTargetParentHandleIsolation(t *testing.T) {
	root := t.TempDir()
	write(t, root, "dir/file", "original")
	target, err := openEditTarget(context.Background(), EditPolicy{}, root, "dir/file")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "old")); err != nil {
		t.Skipf("platform cannot rename open directory: %v", err)
	}
	write(t, root, "dir/file", "replacement")
	info, err := target.parent.Lstat(target.name)
	if err != nil || !os.SameFile(target.info, info) {
		t.Fatalf("opened parent did not retain original directory: %v", err)
	}
	replacement, err := os.Stat(filepath.Join(root, "dir", "file"))
	if err != nil || os.SameFile(target.info, replacement) {
		t.Fatalf("replacement unexpectedly shares original identity: %v", err)
	}
}
