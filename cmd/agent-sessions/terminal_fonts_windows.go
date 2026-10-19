package main

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LOGFONTW is shared by EnumFontFamiliesExW and its callback.
type terminalLogFont struct {
	Height, Width, Escapement, Orientation, Weight int32
	Italic, Underline, StrikeOut, CharSet          byte
	OutPrecision, ClipPrecision, Quality           byte
	PitchAndFamily                                 byte
	FaceName                                       [32]uint16
}

// Windows callback trampolines cannot be freed; reuse one across calls.
// The callback takes typed pointers, which NewCallback allows, rather than
// converting uintptr arguments back to pointers (go vet's unsafeptr check).
var terminalFontCallback = windows.NewCallback(func(lf *terminalLogFont, metric, fontType uintptr, names *[]string) uintptr {
	// LOGFONT's FIXED_PITCH bit (unlike TEXTMETRIC's inverted bit).
	if lf.PitchAndFamily&1 != 0 {
		name := windows.UTF16ToString(lf.FaceName[:])
		// @ families are vertical-writing aliases, not terminal faces.
		if len(name) > 0 && name[0] != '@' {
			*names = append(*names, name)
		}
	}
	return 1 // continue enumeration
})

func nativeTerminalFonts() ([]string, error) {
	gdi := windows.NewLazySystemDLL("gdi32.dll")
	user := windows.NewLazySystemDLL("user32.dll")
	getDC := user.NewProc("GetDC")
	releaseDC := user.NewProc("ReleaseDC")
	enumFonts := gdi.NewProc("EnumFontFamiliesExW")
	dc, _, _ := getDC.Call(0)
	if dc == 0 {
		return nil, errors.New("get local font enumeration device context")
	}
	defer releaseDC.Call(0, dc)
	var names []string
	filter := terminalLogFont{CharSet: 1} // DEFAULT_CHARSET: all installed charsets
	result, _, _ := enumFonts.Call(dc, uintptr(unsafe.Pointer(&filter)), terminalFontCallback, uintptr(unsafe.Pointer(&names)), 0)
	if result == 0 {
		return nil, errors.New("enumerate local GDI font families")
	}
	return names, nil
}
