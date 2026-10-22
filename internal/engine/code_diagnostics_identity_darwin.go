//go:build darwin

package engine

import (
	"fmt"
	"syscall"
	"unsafe"
)

func diagnosticsMachineID() (string, error) {
	var id [16]byte
	timeout := syscall.Timespec{Sec: 1}
	_, _, errno := syscall.Syscall(syscall.SYS_GETHOSTUUID, uintptr(unsafe.Pointer(&id[0])), uintptr(unsafe.Pointer(&timeout)), 0)
	if errno != 0 {
		return "", errno
	}
	return fmt.Sprintf("%x", id), nil
}
