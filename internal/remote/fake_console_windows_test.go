//go:build windows

package remote

import "golang.org/x/sys/windows"

func fakeConsoleVisible() bool {
	hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	if hwnd == 0 {
		return false
	}
	visible, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("IsWindowVisible").Call(hwnd)
	return uint32(visible) != 0
}
