package app

import (
	"runtime"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// fileFilters adapts file dialog filters to the platform the app runs on.
func fileFilters(filters ...wruntime.FileFilter) []wruntime.FileFilter {
	return dialogFilters(runtime.GOOS, filters)
}

// dialogFilters keeps only plain "*.ext" patterns on macOS. Wails turns each
// pattern there into a file type and aborts the app on any pattern without
// one, such as "*.*" or "*.agent-session.zip". With nothing left the dialog
// accepts every file. Other platforms take glob patterns as they are.
func dialogFilters(goos string, filters []wruntime.FileFilter) []wruntime.FileFilter {
	if goos != "darwin" {
		return filters
	}
	var out []wruntime.FileFilter
	for _, f := range filters {
		var keep []string
		for _, p := range strings.Split(f.Pattern, ";") {
			p = strings.TrimSpace(p)
			if ext, ok := strings.CutPrefix(p, "*."); ok && plainExt(ext) {
				keep = append(keep, p)
			}
		}
		if len(keep) > 0 {
			out = append(out, wruntime.FileFilter{DisplayName: f.DisplayName, Pattern: strings.Join(keep, ";")})
		}
	}
	return out
}

func plainExt(ext string) bool {
	if ext == "" {
		return false
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
