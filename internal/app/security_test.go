package app

import "testing"

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
