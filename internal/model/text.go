package model

import (
	"bytes"
	"encoding/json"
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

// TruncateUTF8 cuts s to at most n bytes without splitting a rune.
func TruncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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

// JSONPlainValues walks JSON and keeps only scalar values (dropping object keys),
// returning at most max bytes of space-separated plain text.
func JSONPlainValues(raw json.RawMessage, max int) string {
	if len(raw) == 0 || max <= 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var out strings.Builder
	type frame struct {
		object    bool
		expectKey bool
	}
	stack := make([]frame, 0, 8)
	emit := func(s string) {
		if s == "" {
			return
		}
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(s)
	}

	for out.Len() < max {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, frame{object: true, expectKey: true})
			case '[':
				stack = append(stack, frame{object: false})
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expectKey = true
				}
			}
		case string:
			if len(stack) > 0 {
				top := &stack[len(stack)-1]
				if top.object {
					if top.expectKey {
						top.expectKey = false
						continue
					}
					top.expectKey = true
				}
			}
			emit(t)
		default:
			if len(stack) > 0 && stack[len(stack)-1].object {
				stack[len(stack)-1].expectKey = true
			}
			emit(tokString(tok))
		}
	}
	return TruncateUTF8(OneLine(out.String()), max)
}

func tokString(tok any) string {
	switch t := tok.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// ToolSummary renders a tool call as its name plus a short plain-text summary of input.
func ToolSummary(tool *ToolCall) string {
	if tool == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(tool.Name)
	if summary := JSONPlainValues(tool.Input, 512); summary != "" {
		b.WriteByte(' ')
		b.WriteString(summary)
	}
	return b.String()
}

// ExtractMessageText returns the message's combined text and its kind label ("text", "reasoning", "tool").
func ExtractMessageText(msg *Message, capBytes int) (string, string) {
	if msg == nil {
		return "", ""
	}
	if capBytes <= 0 {
		capBytes = 8192
	}
	var text, reasoning, tools strings.Builder
	hasText, hasReasoning := false, false
	toolCount := 0

	appendSep := func(b *strings.Builder) {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
	}

	for i := range msg.Parts {
		part := &msg.Parts[i]
		switch part.Kind {
		case PartText:
			if part.Text == "" {
				continue
			}
			hasText = true
			appendSep(&text)
			text.WriteString(TruncateUTF8(OneLine(part.Text), capBytes))
		case PartReasoning:
			if part.Text == "" {
				continue
			}
			hasReasoning = true
			appendSep(&reasoning)
			reasoning.WriteString(TruncateUTF8(OneLine(part.Text), capBytes))
		case PartTool:
			if part.Tool == nil {
				continue
			}
			toolCount++
			appendSep(&tools)
			tools.WriteString(TruncateUTF8(OneLine(ToolSummary(part.Tool)), capBytes))
		}
	}
	if toolCount == 0 && !hasText && !hasReasoning {
		return "", ""
	}

	kind := "tool"
	switch {
	case hasText:
		kind = "text"
	case hasReasoning:
		kind = "reasoning"
	}

	joinCapped := func(pieces []string, cap int) string {
		var b strings.Builder
		for _, p := range pieces {
			if p == "" {
				continue
			}
			if b.Len() == 0 {
				if len(p) <= cap {
					b.WriteString(p)
					continue
				}
				b.WriteString(TruncateUTF8(p, cap))
				break
			}
			remaining := cap - b.Len() - 1
			if remaining <= 0 {
				break
			}
			b.WriteByte(' ')
			if len(p) <= remaining {
				b.WriteString(p)
				continue
			}
			b.WriteString(TruncateUTF8(p, remaining))
			break
		}
		return b.String()
	}

	body := joinCapped([]string{text.String(), reasoning.String(), tools.String()}, capBytes)
	return body, kind
}

