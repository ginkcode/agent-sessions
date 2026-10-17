//go:build !windows

package launch

import "errors"

func windowsPowerShell() (string, error) {
	return "", errors.New("Windows PowerShell is only on Windows")
}
