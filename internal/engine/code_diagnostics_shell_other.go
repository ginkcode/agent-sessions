//go:build !darwin && !linux

package engine

// loginShellPath: there is no live route on this platform.
var loginShellPath = func(string) []string { return nil }
