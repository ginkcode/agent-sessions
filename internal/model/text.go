package model

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TruncateRunes truncates a string to at most n UTF-8 runes without cutting runes.
func TruncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// OneLine replaces sequences of whitespace with a single space and trims edges.
func OneLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}
