//go:build !darwin && !linux

package engine

import "errors"

// Code diagnostics run on macOS and Linux hosts only. Other platforms must add
// a machine identity before enabling them.
func diagnosticsMachineID() (string, error) { return "", errors.New("unsupported host identity") }
