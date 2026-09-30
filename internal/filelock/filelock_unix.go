//go:build !windows

package filelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Lock is an exclusive advisory lock (flock) on one file.
type Lock struct {
	f *os.File
}

// TryLock takes the lock on path without waiting; ErrLocked means another
// open file holds it, in this process or another. The file is created 0600
// if missing and a symlink is refused.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("filelock: open %s: %w", path, err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("filelock: flock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock. It is safe on a nil Lock and more than once.
func (l *Lock) Unlock() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	// Closing the only descriptor releases the flock.
	return f.Close()
}
