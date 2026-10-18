//go:build darwin && (webkit2_41 || desktop)

package main

import (
	"strings"
	"testing"
)

func TestNativeTerminalFonts(t *testing.T) {
	names, err := nativeTerminalFonts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("CoreText returned no installed monospace families")
	}
}
