package pathutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathStyle(t *testing.T) {
	tests := []struct {
		in      string
		windows bool
		abs     bool
		clean   string
	}{
		{in: "/home/dev/a/../b/", abs: true, clean: "/home/dev/b"},
		{in: "/", abs: true, clean: "/"},
		{in: "a/b/../c", clean: "a/c"},
		{in: ".", clean: "."},
		{in: "dir name", clean: "dir name"},
		{in: `/odd\name`, abs: true, clean: `/odd\name`},
		{in: `C:\Users\dev\a\..\b\`, windows: true, abs: true, clean: `C:\Users\dev\b`},
		{in: `c:/Users/dev`, windows: true, abs: true, clean: `C:\Users\dev`},
		{in: `C:\`, windows: true, abs: true, clean: `C:\`},
		{in: `C:`, windows: true, clean: `C:`},
		{in: `C:dir\x`, windows: true, clean: `C:dir\x`},
		{in: `\\?\D:\Workspaces\x`, windows: true, abs: true, clean: `D:\Workspaces\x`},
		{in: `\\?\UNC\server\share\x`, windows: true, abs: true, clean: `\\server\share\x`},
		{in: `\\server\share\x\..\y`, windows: true, abs: true, clean: `\\server\share\y`},
		{in: `\\server\share`, windows: true, abs: true, clean: `\\server\share`},
		{in: `\\server\share\x\..\..`, windows: true, abs: true, clean: `\\server\share`},
		{in: `//?/D:/work/x/../y`, windows: true, abs: true, clean: `D:\work\y`},
		{in: `//?/UNC/server/share/x`, windows: true, abs: true, clean: `\\server\share\x`},
		{in: `//home/dev/x/`, abs: true, clean: `/home/dev/x`},
		{in: `///home/dev/x`, abs: true, clean: `/home/dev/x`},
		{in: `C:dir\..\..`, windows: true, clean: `C:..`},
		{in: `\rooted\x`, windows: true, clean: `\rooted\x`},
		{in: `src\main.go`, windows: true, clean: `src\main.go`},
		{in: `src\..\main.go`, windows: true, clean: `main.go`},
	}
	for _, tt := range tests {
		if got := IsWindows(tt.in); got != tt.windows {
			t.Errorf("IsWindows(%q) = %t, want %t", tt.in, got, tt.windows)
		}
		if got := IsAbs(tt.in); got != tt.abs {
			t.Errorf("IsAbs(%q) = %t, want %t", tt.in, got, tt.abs)
		}
		if got := Clean(tt.in); got != tt.clean {
			t.Errorf("Clean(%q) = %q, want %q", tt.in, got, tt.clean)
		}
	}
	if got := Clean(""); got != "" {
		t.Errorf(`Clean("") = %q`, got)
	}
}

func TestRel(t *testing.T) {
	tests := []struct {
		base, target, want string
		ok                 bool
	}{
		{"/home/al", "/home/al", "", true},
		{"/home/al", "/home/al/away/x", "away/x", true},
		{"/home/al/", "/home/al/away", "away", true},
		{"/", "/etc", "etc", true},
		{"/home/al", "/home/al2", "", false},
		{"/home/al", "/home", "", false},
		{"/home/al", "away", "", false},
		{`C:\Users\Al`, `c:\users\al\Work\x`, `Work\x`, true},
		{`C:\Users\Al`, `\\?\C:\Users\Al\Work`, "Work", true},
		{`C:\`, `C:\Work`, "Work", true},
		{`C:\Users\Al`, `C:\Users\Al2`, "", false},
		{`C:\Users\Al`, `D:\Users\Al`, "", false},
		{`C:\Users\Al`, "/Users/Al", "", false},
		{"/home/al", `C:\home\al`, "", false},
	}
	for _, tt := range tests {
		got, ok := Rel(tt.base, tt.target)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Rel(%q, %q) = %q, %t; want %q, %t", tt.base, tt.target, got, ok, tt.want, tt.ok)
		}
	}
}

func TestForeignPaths(t *testing.T) {
	foreign := `C:\Users\dev\project`
	if runtime.GOOS == "windows" {
		foreign = "/home/dev/project"
	}
	if got := NormalizeDir(foreign + string(Separator(foreign))); got != foreign {
		t.Errorf("NormalizeDir(%q) = %q", foreign, got)
	}
	if Exists(foreign) {
		t.Errorf("Exists(%q) = true for a path in the other OS's style", foreign)
	}
	if _, ok := NewGitResolver().Resolve(foreign); ok {
		t.Errorf("Resolve(%q) found a repository", foreign)
	}
	if native := t.TempDir(); !filepath.IsAbs(NormalizeDir(native)) || !Exists(native) {
		t.Errorf("native path %q: NormalizeDir = %q, Exists = %t", native, NormalizeDir(native), Exists(native))
	}
}
