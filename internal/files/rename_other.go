//go:build !linux && !darwin && !windows

package files

import (
	"errors"
	"os"
)

// Fail closed rather than emulate no-replace using a racy existence check.
func renameNoReplace(_ *os.File, _, _ string) error {
	return errors.New("safe no-replace rename is not supported on this platform")
}
