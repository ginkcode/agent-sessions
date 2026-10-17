package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestDropFilesSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake host runs /bin/sh")
	}
	src := t.TempDir()
	var paths []string
	for name, data := range map[string]string{"a/shot 1.png": "one", "b/shot 1.png": "two", "it's.jpg": "three"} {
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths = []string{filepath.Join(src, "a/shot 1.png"), filepath.Join(src, "b/shot 1.png"), filepath.Join(src, "it's.jpg")}
	tmp := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", tmp+"/")
	bin := fakeSSHPath(t, "shell", nil)

	got, err := DropFiles(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("paths = %q", got)
	}
	dir := filepath.Dir(got[0])
	if filepath.Dir(dir) != tmp || !strings.HasPrefix(filepath.Base(dir), "agent-sessions-drop.") {
		t.Errorf("upload directory = %q, want one in %q", dir, tmp)
	}
	if want := []string{dir + "/shot 1.png", dir + "/shot 1-2.png", dir + "/it's.jpg"}; !slices.Equal(got, want) {
		t.Errorf("paths = %q, want %q", got, want)
	}
	for i, want := range []string{"one", "two", "three"} {
		data, err := os.ReadFile(got[i])
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", got[i], data, err, want)
		}
		if info, err := os.Stat(got[i]); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", got[i], info.Mode())
		}
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("upload directory mode = %v, %v", info.Mode(), err)
	}

	// Folders and missing files copy nothing.
	if _, err := DropFiles(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, []string{src}); !errors.Is(err, ErrDropFolder) {
		t.Errorf("folder: %v", err)
	}
	if _, err := DropFiles(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, []string{filepath.Join(src, "gone.png")}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 1 {
		t.Errorf("uploads = %v, want only the first", entries)
	}
}

func TestDropFilesSSHFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := fakeSSHPath(t, "auth-fail", nil)
	_, err := DropFiles(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, []string{src})
	if err == nil || !strings.Contains(err.Error(), "copy the dropped files to box") {
		t.Errorf("err = %v", err)
	}
}

func TestDropFilesWSL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake distribution runs /bin/sh")
	}
	tools := t.TempDir()
	wslpath := `#!/bin/sh
[ "$1" = -u ] || exit 2
case $2 in
'C:\'*) printf '/mnt/c/%s\n' "$(printf '%s' "${2#C:\\}" | tr '\\' /)" ;;
*) echo "wslpath: $2: not a Windows path" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(tools, "wslpath"), []byte(wslpath), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	bin := fakeSSHPath(t, "shell", nil)
	old := wslBinary
	wslBinary = func() (string, error) { return bin, nil }
	t.Cleanup(func() { wslBinary = old })

	got, err := DropFiles(context.Background(), WSLTarget("Ubuntu"), SSHOptions{}, []string{`C:\Users\me\shot 1.png`, `C:\it's.jpg`})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/mnt/c/Users/me/shot 1.png", "/mnt/c/it's.jpg"}; !slices.Equal(got, want) {
		t.Errorf("paths = %q, want %q", got, want)
	}
	if _, err := DropFiles(context.Background(), WSLTarget("Ubuntu"), SSHOptions{}, []string{`D:\x.png`}); err == nil || !strings.Contains(err.Error(), "not a Windows path") {
		t.Errorf("untranslatable path: %v", err)
	}
}

func TestDropLines(t *testing.T) {
	out := "Welcome!\r\n" + dropMarker + "/tmp/a\r\nnoise " + dropMarker + "x\n" + dropMarker + "/tmp/b c\n"
	if got := dropLines(out); !slices.Equal(got, []string{"/tmp/a", "/tmp/b c"}) {
		t.Errorf("dropLines = %q", got)
	}
}
