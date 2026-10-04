// Package platform lets tests skip or relax checks that an OS cannot support,
// so the same suite runs on Linux, macOS and Windows.
package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// ModeBits reports whether file permission bits are enforced. On Windows Go
// reports 0666/0777 and access is governed by the profile's ACLs instead, so
// tests assert modes only when this is true.
const ModeBits = runtime.GOOS != "windows"

// TempDir returns t.TempDir() with symlinks and Windows 8.3 short names
// resolved, the form code that normalizes paths reports. On macOS the temp
// dir is under the /var symlink; a Windows runner's TEMP may be
// C:\Users\RUNNER~1.
func TempDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Symlink creates newname pointing at oldname, skipping the test where the
// OS refuses: Windows needs Developer Mode or an elevated process.
func Symlink(t testing.TB, oldname, newname string) {
	t.Helper()
	err := os.Symlink(oldname, newname)
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" && errors.Is(err, errPrivilegeNotHeld) {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Fatal(err)
}

// errPrivilegeNotHeld is Windows' ERROR_PRIVILEGE_NOT_HELD.
const errPrivilegeNotHeld = syscall.Errno(1314)

// SkipWithoutModeBits skips a test whose subject is permission bits.
func SkipWithoutModeBits(t testing.TB) {
	t.Helper()
	if !ModeBits {
		t.Skip("permission bits are not enforced on " + runtime.GOOS)
	}
}

// RequireCommand skips the test when name is not on PATH, such as /bin/sh
// on Windows or the go tool on a machine that only runs test binaries.
func RequireCommand(t testing.TB, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available: %v", name, err)
	}
}
