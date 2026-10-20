//go:build !darwin && (webkit2_41 || desktop)

package main

// hookWindowCycling is native on macOS only. On Windows and Linux the page
// sees Alt+` itself and calls FocusNextWindow (see App.svelte).
func hookWindowCycling(func(forward bool)) {}
