package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTerminalFonts(t *testing.T) {
	got := normalizeTerminalFonts([]string{" Menlo ", "", "\r\n", "JetBrainsMono Nerd Font", "Menlo", "Fira Code\r", "  日本語 Mono  "})
	want := []string{"Fira Code", "JetBrainsMono Nerd Font", "Menlo", "日本語 Mono"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fonts = %q, want %q", got, want)
	}
	if got := normalizeTerminalFonts(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty fonts should be a non-nil empty list, got %#v", got)
	}
}

func TestTerminalFonts(t *testing.T) {
	terminalFontProvider.RLock()
	previous := terminalFontProvider.list
	terminalFontProvider.RUnlock()
	t.Cleanup(func() { SetNativeTerminalFonts(previous) })

	// No application startup or selected-host state is needed: enumeration is
	// always a local desktop operation, even while a remote host is selected.
	application := &App{}
	SetNativeTerminalFonts(nil)
	if _, err := application.TerminalFonts(); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing provider: %v", err)
	}

	failure := errors.New("font service failed")
	SetNativeTerminalFonts(func() ([]string, error) { return []string{"partial"}, failure })
	if fonts, err := application.TerminalFonts(); fonts != nil || !errors.Is(err, failure) {
		t.Fatalf("provider error: fonts=%v err=%v", fonts, err)
	}

	SetNativeTerminalFonts(func() ([]string, error) {
		return []string{"Menlo", " Fira Code ", "Menlo", ""}, nil
	})
	fonts, err := application.TerminalFonts()
	if err != nil || !reflect.DeepEqual(fonts, []string{"Fira Code", "Menlo"}) {
		t.Fatalf("provider success: fonts=%v err=%v", fonts, err)
	}

	SetNativeTerminalFonts(func() ([]string, error) { return nil, nil })
	fonts, err = application.TerminalFonts()
	if err != nil || fonts == nil || len(fonts) != 0 {
		t.Fatalf("empty provider: fonts=%#v err=%v", fonts, err)
	}
}
