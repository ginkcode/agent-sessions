//go:build darwin && (webkit2_41 || desktop)

package main

/*
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
int print_web_view(void);
*/
import "C"

import "errors"

// nativePrint prints the window in portrait with margins (print_darwin.m);
// Wails' own WindowPrint forces landscape with no margins on macOS.
func nativePrint() error {
	if C.print_web_view() != 0 {
		return errors.New("print: printing needs macOS 11 or later")
	}
	return nil
}

func nativePrinter() func() error { return nativePrint }
