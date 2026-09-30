// Package filelock provides advisory locks on files, shared by every process
// of one user: desktop apps and remote servers of any version.
//
// A lock is held by an open file, so it is released when the holder exits,
// however it exits. Lock files are never deleted: removing one while another
// process has it open would let a third process lock a new file at the same
// path while the first still holds the old one.
package filelock

import (
	"errors"
	"fmt"
	"time"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("filelock: held by another process")

// retryEvery is how often Acquire tries the lock again.
const retryEvery = 20 * time.Millisecond

// Acquire waits up to timeout for the lock on path. Use it only around short
// critical sections. The holder can be another process that has stalled, so
// the wait is bounded: after timeout it returns an error wrapping ErrLocked.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		l, err := TryLock(path)
		if !errors.Is(err, ErrLocked) {
			return l, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: gave up after %v", err, timeout)
		}
		time.Sleep(retryEvery)
	}
}
