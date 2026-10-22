//go:build !darwin

package engine

import "errors"

// Native checker tuples are currently verified only on darwin/arm64. Other
// platforms must add verified host identity acquisition before enabling them.
func diagnosticsMachineID() (string, error) { return "", errors.New("unsupported host identity") }
