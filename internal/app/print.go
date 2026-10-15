package app

import (
	"errors"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// nativePrint replaces Wails' WindowPrint where the desktop app prints
// natively (macOS); nil elsewhere.
var nativePrint func() error

// SetNativePrint sets how PrintWindow opens the print dialog; nil uses
// Wails' WindowPrint. It is a package function so Wails doesn't bind it.
func SetNativePrint(fn func() error) {
	nativePrint = fn
}

// PrintWindow opens the system print dialog for the window, where it can
// also be saved as a PDF. The page's print styles decide what is printed.
// macOS needs this: its web view ignores window.print().
func (a *App) PrintWindow() error {
	if a.ctx == nil {
		return errors.New("print: the window is not ready")
	}
	if nativePrint != nil {
		return nativePrint()
	}
	wruntime.WindowPrint(a.ctx)
	return nil
}
