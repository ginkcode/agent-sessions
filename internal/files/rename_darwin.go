package files

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameNoReplace(parent *os.File, source, name string) error {
	return unix.RenameatxNp(int(parent.Fd()), source, int(parent.Fd()), name, unix.RENAME_EXCL)
}
