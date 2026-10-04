//go:build !windows

package remote

func fakeConsoleVisible() bool { return false }
