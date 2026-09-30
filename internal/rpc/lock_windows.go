//go:build windows

package rpc

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock represents an exclusive advisory lock on the server cache.
type Lock struct {
	file *os.File
}

// AcquireCacheLock attempts to acquire an exclusive advisory lock on serve.lock.
func AcquireCacheLock(cacheDir string) (*Lock, error) {
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	lockPath := filepath.Join(cacheDir, "serve.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrRemoteBusy
		}
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	return &Lock{file: f}, nil
}

// Unlock releases the advisory lock and closes the lock file.
func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	path := l.file.Name()
	_ = l.file.Close()
	_ = os.Remove(path)
	return nil
}
