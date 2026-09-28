//go:build unix || linux || darwin

package index

import (
	"sync"
	"syscall"
)

var umaskMu sync.Mutex

func setPrivateUmask() func() {
	umaskMu.Lock()
	old := syscall.Umask(0o077)
	return func() {
		syscall.Umask(old)
		umaskMu.Unlock()
	}
}
