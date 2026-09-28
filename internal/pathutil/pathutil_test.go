package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDir(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(root)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "root", in: volume + string(filepath.Separator), want: volume + string(filepath.Separator)},
		{name: "clean", in: filepath.Join(root, "a", "..", "b"), want: filepath.Join(root, "b")},
		{name: "existing dir", in: realDir, want: realDir},
		{name: "trailing slash", in: realDir + string(filepath.Separator), want: realDir},
		{name: "symlink resolved", in: link, want: realDir},
		{name: "symlink trailing slash", in: link + string(filepath.Separator), want: realDir},
		{name: "missing dir kept", in: filepath.Join(root, "missing"), want: filepath.Join(root, "missing")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeDir(tt.in); got != tt.want {
				t.Errorf("NormalizeDir(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExists(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "nope"), broken); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "dir", in: root, want: true},
		{name: "file", in: file, want: true},
		{name: "missing", in: filepath.Join(root, "missing"), want: false},
		{name: "broken symlink", in: broken, want: false},
		{name: "empty", in: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Exists(tt.in); got != tt.want {
				t.Errorf("Exists(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
