package files

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEncodeEditTextNoOpRoundTrip(t *testing.T) {
	bodies := []string{
		"", "\uFEFF", "one line", "\uFEFFone line", "\n", "\r\n",
		"a\nb", "a\nb\n", "a\r\nb", "a\r\nb\r\n",
		"\uFEFFa\nb\n", "\uFEFFa\r\nb", "\uFEFFa\r\nb\r\n",
		"héllo\n第二行\n🙂\n", "héllo\r\n第二行\r\n🙂",
		"a\uFEFF\n", "\uFEFF\uFEFFa\n", "a\n\n", "a\r\n\r\n",
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			original := []byte(body)
			format := textEditInfo(original)
			if format.Eligible || format.Reason != EditNotChecked {
				t.Fatalf("expected safety-pending format, got %+v", format)
			}
			// Opening the editor removes only the original leading BOM and
			// normalizes CRLF. Content itself remains the unchanged raw bytes.
			text := strings.ReplaceAll(string(bytes.TrimPrefix(original, utf8BOM)), "\r\n", "\n")
			got, err := encodeEditText(text, *format)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, original) {
				t.Errorf("encoded = %q, want %q", got, original)
			}
		})
	}
}

func TestEncodeEditTextChanges(t *testing.T) {
	tests := []struct {
		name, text string
		format     EditInfo
		want       string
	}{
		{"none uses lf", "a\nb\n", EditInfo{Newline: NewlineNone}, "a\nb\n"},
		{"crlf removes final newline", "a\nb", EditInfo{Newline: NewlineCRLF}, "a\r\nb"},
		{"crlf adds final newline", "a\nb\n", EditInfo{Newline: NewlineCRLF}, "a\r\nb\r\n"},
		{"lf empty", "", EditInfo{Newline: NewlineLF}, ""},
		{"crlf empty retains bom", "", EditInfo{Newline: NewlineCRLF, BOM: true}, "\uFEFF"},
		{"literal leading bom retained", "\uFEFFa", EditInfo{Newline: NewlineNone, BOM: true}, "\uFEFF\uFEFFa"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeEditText(tt.text, tt.format)
			if err != nil || string(got) != tt.want {
				t.Fatalf("encodeEditText = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestEncodeEditTextRejectsUnsupported(t *testing.T) {
	tests := []struct{ name, text, newline string }{
		{"bare cr", "a\rb", NewlineLF},
		{"trailing cr", "a\r", NewlineCRLF},
		{"editor crlf", "a\r\nb", NewlineCRLF},
		{"mixed editor newlines", "a\nb\r\n", NewlineLF},
		{"invalid utf8", "a\xff", NewlineLF},
		{"truncated utf8", "\xe2\x82", NewlineNone},
		{"binary nul", "a\x00b", NewlineNone},
		{"missing newline metadata", "a", ""},
		{"mixed newline metadata", "a\nb", "mixed"},
		{"bare cr metadata", "a", "cr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeEditText(tt.text, EditInfo{Eligible: true, Newline: tt.newline})
			if !errors.Is(err, ErrUnsupportedEditText) || got != nil {
				t.Fatalf("encodeEditText = %q, %v; want nil, ErrUnsupportedEditText", got, err)
			}
		})
	}
}

func TestEncodeEditTextByteLimits(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		format EditInfo
		size   int
	}{
		{"ascii limit", strings.Repeat("x", MaxEditBytes), EditInfo{Newline: NewlineLF}, MaxEditBytes},
		{"ascii over", strings.Repeat("x", MaxEditBytes+1), EditInfo{Newline: NewlineLF}, MaxEditBytes + 1},
		{"unicode limit", strings.Repeat("é", MaxEditBytes/2), EditInfo{Newline: NewlineNone}, MaxEditBytes},
		{"unicode over", strings.Repeat("é", MaxEditBytes/2) + "x", EditInfo{Newline: NewlineNone}, MaxEditBytes + 1},
		{"bom limit", strings.Repeat("x", MaxEditBytes-3), EditInfo{Newline: NewlineNone, BOM: true}, MaxEditBytes},
		{"bom over", strings.Repeat("x", MaxEditBytes-2), EditInfo{Newline: NewlineNone, BOM: true}, MaxEditBytes + 1},
		{"crlf limit", strings.Repeat("\n", MaxEditBytes/2), EditInfo{Newline: NewlineCRLF}, MaxEditBytes},
		{"crlf expansion over", strings.Repeat("\n", MaxEditBytes/2) + "x", EditInfo{Newline: NewlineCRLF}, MaxEditBytes + 1},
		{"bom crlf limit", strings.Repeat("\n", (MaxEditBytes-3)/2) + "x", EditInfo{Newline: NewlineCRLF, BOM: true}, MaxEditBytes},
		{"bom crlf over", strings.Repeat("\n", (MaxEditBytes-3)/2) + "xx", EditInfo{Newline: NewlineCRLF, BOM: true}, MaxEditBytes + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeEditText(tt.text, tt.format)
			if tt.size > MaxEditBytes {
				if !errors.Is(err, ErrEditTooLarge) || got != nil {
					t.Fatalf("got %d bytes, %v; want nil, ErrEditTooLarge", len(got), err)
				}
			} else if err != nil || len(got) != tt.size {
				t.Fatalf("got %d bytes, %v; want %d bytes, nil", len(got), err, tt.size)
			}
		})
	}
}

func TestSaveContractJSON(t *testing.T) {
	request := SaveRequest{Path: "a.txt", Text: "", ExpectedRevision: revisionOf(nil)}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"path":"a.txt","text":"","expectedRevision":"` + request.ExpectedRevision + `"}`
	if string(data) != want {
		t.Errorf("request JSON = %s, want %s", data, want)
	}
	for _, result := range []SaveResult{
		{Status: SaveStatusSaved, Content: &Content{Path: "a.txt", Revision: revisionOf(nil)}},
		{Status: SaveStatusConflict, ObservedRevision: revisionOf([]byte("changed"))},
		{Status: SaveStatusConflict},
	} {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		var got SaveResult
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status != result.Status || got.ObservedRevision != result.ObservedRevision {
			t.Errorf("result round trip = %+v, want %+v", got, result)
		}
		if result.Status == SaveStatusSaved {
			if got.Content == nil || got.Content.Path != result.Content.Path || got.Content.Revision != result.Content.Revision {
				t.Errorf("saved content = %+v, want %+v", got.Content, result.Content)
			}
		} else if got.Content != nil || strings.Contains(string(data), `"content"`) {
			t.Errorf("conflict contains success content: %s", data)
		}
		if result.ObservedRevision == "" && strings.Contains(string(data), `"observedRevision"`) {
			t.Errorf("empty observedRevision not omitted: %s", data)
		}
	}
}
