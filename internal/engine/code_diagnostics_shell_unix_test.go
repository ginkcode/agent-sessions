//go:build darwin || linux

package engine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The login shell runs in the home directory with -l -i, and only absolute,
// clean PATH entries are kept.
func TestReadLoginShellPath(t *testing.T) {
	if !localLiveSupported() {
		t.Skip("no login shell route")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	shell := filepath.Join(t.TempDir(), "fake-shell")
	script := "#!/bin/sh\n" +
		"[ \"$1 $2 $3\" = \"-l -i -c\" ] || exit 1\n" +
		"[ \"$(pwd -P)\" = \"" + home + "\" ] || exit 1\n" +
		"echo noise from startup files\n" +
		"PATH=/login/bin:relative:/a/../b:/usr/bin; export PATH\n" +
		"exec /bin/sh -c \"$4\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	if got := readLoginShellPath(home); !slices.Equal(got, []string{"/login/bin", "/usr/bin"}) {
		t.Fatal(got)
	}
	// A shell that prints no marker contributes nothing.
	if err := os.WriteFile(shell, []byte("#!/bin/sh\necho /evil/bin\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := readLoginShellPath(home); got != nil {
		t.Fatal(got)
	}
	if got := readLoginShellPath("relative"); got != nil {
		t.Fatal(got)
	}
}
