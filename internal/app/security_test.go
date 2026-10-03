package app

import (
	"strings"
	"testing"
)

func TestOpenURLRejectsUnsafeSchemes(t *testing.T) {
	a := &App{}
	for _, raw := range []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,<script>alert(1)</script>",
		"mailto:test@example.com",
		"", // empty URL
	} {
		if err := a.OpenURL(raw); err == nil {
			t.Errorf("OpenURL(%q) should reject unsafe or empty URL", raw)
		}
	}
}

func TestDesktopOpen(t *testing.T) {
	const dir, link = "/home/dev/.claude/projects/x", "https://example.com/?a=1&b=^2"
	tests := []struct {
		goos, target string
		isURL        bool
		want         []string
	}{
		{"linux", dir, false, []string{"xdg-open", dir}},
		{"linux", link, true, []string{"xdg-open", link}},
		{"darwin", dir, false, []string{"open", dir}},
		{"darwin", link, true, []string{"open", link}},
		{"windows", `C:\Users\dev\.claude\projects\x`, false, []string{"explorer", `C:\Users\dev\.claude\projects\x`}},
		{"windows", link, true, []string{"rundll32", "url.dll,FileProtocolHandler", link}},
	}
	for _, tt := range tests {
		got := desktopOpen(tt.goos, tt.target, tt.isURL).Args
		if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
			t.Errorf("desktopOpen(%q, %q, %t) = %q, want %q", tt.goos, tt.target, tt.isURL, got, tt.want)
		}
	}
}
