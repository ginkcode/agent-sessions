//go:build darwin && (webkit2_41 || desktop)

package main

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreText
#include <stdlib.h>
char *terminal_font_families(void);
*/
import "C"

import (
	"errors"
	"strings"
	"unsafe"
)

func nativeTerminalFonts() ([]string, error) {
	names := C.terminal_font_families()
	if names == nil {
		return nil, errors.New("enumerate local CoreText font families")
	}
	defer C.free(unsafe.Pointer(names))
	return strings.Split(C.GoString(names), "\n"), nil
}
