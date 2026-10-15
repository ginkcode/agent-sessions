//go:build !darwin && (webkit2_41 || desktop)

package main

// nativePrinter is nil off macOS: the WebView2 and WebKitGTK print dialogs
// already offer the orientation, so Wails' WindowPrint is used as it is.
func nativePrinter() func() error { return nil }
