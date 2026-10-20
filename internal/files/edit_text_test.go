package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// sha is the revision expected for body, computed independently of
// revisionOf.
func sha(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestTextEditInfo(t *testing.T) {
	bom := "\xEF\xBB\xBF"
	atLimit := strings.Repeat("a\n", MaxEditBytes/2)
	tests := []struct {
		name    string
		body    string
		newline string
		bom     bool
		reason  string
	}{
		{"empty", "", NewlineNone, false, EditNotChecked},
		{"bom only", bom, NewlineNone, true, EditNotChecked},
		{"no newline", "one line", NewlineNone, false, EditNotChecked},
		{"lf", "a\nb\n", NewlineLF, false, EditNotChecked},
		{"lf no final newline", "a\nb", NewlineLF, false, EditNotChecked},
		{"lf only newline", "\n", NewlineLF, false, EditNotChecked},
		{"crlf", "a\r\nb\r\n", NewlineCRLF, false, EditNotChecked},
		{"crlf no final newline", "a\r\nb", NewlineCRLF, false, EditNotChecked},
		{"crlf only newline", "\r\n", NewlineCRLF, false, EditNotChecked},
		{"bom lf", bom + "a\nb\n", NewlineLF, true, EditNotChecked},
		{"bom crlf", bom + "a\r\nb\r\n", NewlineCRLF, true, EditNotChecked},
		{"bom not at start", "a" + bom + "\n", NewlineLF, false, EditNotChecked},
		{"unicode lf", "héllo\n第二行\n🙂\n", NewlineLF, false, EditNotChecked},
		{"unicode crlf", "héllo\r\n第二行\r\n", NewlineCRLF, false, EditNotChecked},
		{"mixed lf then crlf", "a\nb\r\n", "", false, EditUnsupportedNewlines},
		{"mixed crlf then lf", "a\r\nb\n", "", false, EditUnsupportedNewlines},
		{"bom mixed", bom + "a\r\nb\n", "", true, EditUnsupportedNewlines},
		{"bare cr", "a\rb\r", "", false, EditUnsupportedNewlines},
		{"bare cr at end", "a\r", "", false, EditUnsupportedNewlines},
		{"cr cr lf", "a\r\r\n", "", false, EditUnsupportedNewlines},
		{"bare cr with lf", "a\rb\n", "", false, EditUnsupportedNewlines},
		{"bare cr with crlf", "a\r\nb\rc", "", false, EditUnsupportedNewlines},
		{"at edit limit", atLimit, NewlineLF, false, EditNotChecked},
		{"over edit limit", atLimit + "x", NewlineLF, false, EditTooLarge},
		{"over edit limit crlf", strings.Repeat("a\r\n", MaxEditBytes/3+1), NewlineCRLF, false, EditTooLarge},
		// Size is reported before newlines; the newline style is still unset.
		{"over edit limit mixed", atLimit + "\r\n", "", false, EditTooLarge},
	}
	if len(atLimit) != MaxEditBytes {
		t.Fatalf("atLimit is %d bytes, want %d", len(atLimit), MaxEditBytes)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := textEditInfo([]byte(tt.body))
			want := EditInfo{Eligible: false, Reason: tt.reason, Newline: tt.newline, BOM: tt.bom}
			if got == nil || *got != want {
				t.Errorf("textEditInfo = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRevisionOfRawBytes(t *testing.T) {
	if got, want := revisionOf(nil), "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"; got != want {
		t.Errorf("revisionOf(empty) = %s, want %s", got, want)
	}
	// Bytes a text-level comparison would treat as equal hash differently.
	bodies := []string{"a\n", "a\r\n", "\xEF\xBB\xBFa\n", "a", "é\n", "e\u0301\n"}
	seen := map[string]string{}
	for _, b := range bodies {
		rev := revisionOf([]byte(b))
		if rev != sha(b) {
			t.Errorf("revisionOf(%q) = %s, want %s", b, rev, sha(b))
		}
		if other, ok := seen[rev]; ok {
			t.Errorf("revisionOf(%q) = revisionOf(%q)", b, other)
		}
		seen[rev] = b
	}
}

func TestReadEditMetadata(t *testing.T) {
	root := t.TempDir()
	bom := "\xEF\xBB\xBF"
	tests := []struct {
		path    string
		body    string
		newline string
		bom     bool
		reason  string
	}{
		{"empty.txt", "", NewlineNone, false, EditNotChecked},
		{"lf.go", "package a\n", NewlineLF, false, EditNotChecked},
		{"crlf.txt", "a\r\nb\r\n", NewlineCRLF, false, EditNotChecked},
		{"nofinal.txt", "a\nb", NewlineLF, false, EditNotChecked},
		{"bom.txt", bom + "a\r\nb", NewlineCRLF, true, EditNotChecked},
		{"unicode.md", "# 标题\n\nhéllo 🙂\n", NewlineLF, false, EditNotChecked},
		{"mixed.txt", "a\nb\r\n", "", false, EditUnsupportedNewlines},
		{"cr.txt", "a\rb", "", false, EditUnsupportedNewlines},
		{"limit.txt", strings.Repeat("x", MaxEditBytes), NewlineNone, false, EditNotChecked},
		{"over.txt", strings.Repeat("x", MaxEditBytes+1), NewlineNone, false, EditTooLarge},
		// Above the edit limit but within the viewer's: text and revision
		// are still returned.
		{"readmax.txt", strings.Repeat("x", MaxReadBytes), NewlineNone, false, EditTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			write(t, root, tt.path, tt.body)
			got, err := Read(context.Background(), root, tt.path)
			if err != nil {
				t.Fatal(err)
			}
			// The text is returned byte for byte: BOM and CRs kept.
			if got.Text != tt.body || got.Binary || got.TooLarge {
				t.Fatalf("Read text changed or missing: binary %v, tooLarge %v, %d bytes, want %d",
					got.Binary, got.TooLarge, len(got.Text), len(tt.body))
			}
			if got.Size != int64(len(tt.body)) {
				t.Errorf("size = %d, want %d", got.Size, len(tt.body))
			}
			if got.Revision != sha(tt.body) {
				t.Errorf("revision = %s, want %s", got.Revision, sha(tt.body))
			}
			want := EditInfo{Reason: tt.reason, Newline: tt.newline, BOM: tt.bom}
			if got.Edit == nil || *got.Edit != want {
				t.Errorf("edit = %+v, want %+v", got.Edit, want)
			}
		})
	}
}

func TestReadNoEditMetadataWithoutText(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bin.dat", "text\x00binary")
	write(t, root, "latin1.txt", "caf\xe9\n")
	write(t, root, "logo.png", "\x89PNG\x00\x01")
	write(t, root, "big.txt", strings.Repeat("x", MaxReadBytes+1))
	for _, path := range []string{"bin.dat", "latin1.txt", "logo.png", "big.txt"} {
		got, err := Read(context.Background(), root, path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Text != "" || got.Revision != "" || got.Edit != nil {
			t.Errorf("%s: text %d bytes, revision %q, edit %+v; want none", path, len(got.Text), got.Revision, got.Edit)
		}
	}
}

func TestEmptyContentTextJSON(t *testing.T) {
	data, err := json.Marshal(Content{Revision: sha(""), Edit: textEditInfo(nil)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if text, ok := decoded["text"]; !ok || text != "" {
		t.Fatalf("empty text must survive the bridge: %s", data)
	}
}

func TestContentEditJSON(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "\xEF\xBB\xBFa\r\n")
	c, err := Read(context.Background(), root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["revision"] != sha("\xEF\xBB\xBFa\r\n") {
		t.Errorf("revision = %v", got["revision"])
	}
	edit, ok := got["edit"].(map[string]any)
	if !ok {
		t.Fatalf("edit = %v, want an object", got["edit"])
	}
	// eligible is always present, so a client never has to guess.
	if eligible, ok := edit["eligible"].(bool); !ok || eligible {
		t.Errorf("edit.eligible = %v, want explicit false", edit["eligible"])
	}
	if edit["reason"] != EditNotChecked || edit["newline"] != NewlineCRLF || edit["bom"] != true {
		t.Errorf("edit = %v", edit)
	}

	write(t, root, "bin.dat", "\x00")
	c, err = Read(context.Background(), root, "bin.dat")
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(data); strings.Contains(s, `"revision"`) || strings.Contains(s, `"edit"`) {
		t.Errorf("binary JSON = %s, want no revision or edit", s)
	}
}
