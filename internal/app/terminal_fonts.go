package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var terminalFontProvider struct {
	sync.RWMutex
	list func() ([]string, error)
}

// SetNativeTerminalFonts registers the local desktop's installed-font enumerator.
// It is a package function so Wails does not expose provider registration.
// Passing nil removes the provider. Register it before starting the desktop.
func SetNativeTerminalFonts(fn func() ([]string, error)) {
	terminalFontProvider.Lock()
	defer terminalFontProvider.Unlock()
	terminalFontProvider.list = fn
}

// TerminalFonts returns sorted, unique installed monospace font family names.
// Fonts always belong to the local desktop, never the selected remote host.
// Only names are returned; no font files are read or transferred to the client.
func (a *App) TerminalFonts() ([]string, error) {
	terminalFontProvider.RLock()
	list := terminalFontProvider.list
	terminalFontProvider.RUnlock()
	if list == nil {
		return nil, errors.New("terminal fonts: local font provider is unavailable")
	}
	names, err := list()
	if err != nil {
		return nil, fmt.Errorf("terminal fonts: %w", err)
	}
	return normalizeTerminalFonts(names), nil
}

func normalizeTerminalFonts(names []string) []string {
	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			unique[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for name := range unique {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
