//go:build !windows

package rpc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock represents an exclusive advisory lock on the server cache.
type Lock struct {
	file *os.File
}

// AcquireCacheLock attempts to acquire an exclusive, non-blocking advisory lock on serve.lock.
// If the lock is held by another running process, ErrRemoteBusy is returned.
func AcquireCacheLock(cacheDir string) (*Lock, error) {
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	lockPath := filepath.Join(cacheDir, "serve.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrRemoteBusy
		}
		return nil, fmt.Errorf("flock: %w", err)
	}

	return &Lock{file: f}, nil
}

// Unlock releases the advisory lock and closes the lock file.
func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	defer l.file.Close()
	return syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
}
