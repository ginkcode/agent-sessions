//go:build unix

package remote

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// checkPrivateDir requires dir to be a real directory (not a symlink) owned
// by this user with no group or other permissions.
func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot read owner")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("owned by uid %d, not %d", st.Uid, os.Getuid())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("mode %#o, want 0700", perm)
	}
	return nil
}
