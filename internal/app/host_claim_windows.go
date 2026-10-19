//go:build windows

package app

import "golang.org/x/sys/windows"

var procAllowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// asfwAny lets any process take the foreground (ASFW_ANY).
const asfwAny = ^uintptr(0)

// allowForegroundHandoff lets the window asked to come to the front take
// the foreground from this one: Windows refuses SetForegroundWindow to a
// background process unless the foreground process allows it.
func allowForegroundHandoff() {
	if procAllowSetForegroundWindow.Find() == nil {
		_, _, _ = procAllowSetForegroundWindow.Call(asfwAny)
	}
}
