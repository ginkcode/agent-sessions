//go:build !linux && (webkit2_41 || desktop)

package main

// disableFontHinting is Linux-only; other platforms render text natively.
func disableFontHinting() {}
