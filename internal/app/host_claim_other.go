//go:build !windows

package app

// allowForegroundHandoff is only needed on Windows.
func allowForegroundHandoff() {}
