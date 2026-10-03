package pathutil

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func TestNormalizeDir(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	platform.Symlink(t, realDir, link)
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
	platform.Symlink(t, filepath.Join(root, "nope"), broken)

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

func TestSQLiteURI(t *testing.T) {
	params := url.Values{"mode": {"ro"}}
	tests := []struct{ path, want string }{
		{"/home/dev/a b.db", "file:///home/dev/a%20b.db?mode=ro"},
	}
	if runtime.GOOS == "windows" {
		tests = []struct{ path, want string }{
			{`C:\Users\dev\a b.db`, "file:///C:/Users/dev/a%20b.db?mode=ro"},
			{`\\server\share\x.db`, "file:////server/share/x.db?mode=ro"},
		}
	}
	for _, tt := range tests {
		if got := SQLiteURI(tt.path, params); got != tt.want {
			t.Errorf("SQLiteURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
