package files

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// SaveRequest is a snapshot of an editor document to save to an existing file.
// It does not authorize a write: a save must independently recheck the path,
// target eligibility, format, byte limits and revision before replacing bytes.
// There is deliberately no unchecked force-write option.
type SaveRequest struct {
	Path string `json:"path"`
	// Text is valid UTF-8 with LF-only line breaks, excluding the original
	// leading BOM. Any intended final newline is part of this snapshot.
	Text string `json:"text"`
	// ExpectedRevision is required. A save must validate the nonempty
	// "sha256:" prefix followed by exactly 64 lowercase hexadecimal digits,
	// then compare it with the revision of the current raw file bytes.
	ExpectedRevision string `json:"expectedRevision"`
}

// SaveStatus discriminates a SaveResult; failures other than revision conflicts
// are returned as errors, not additional result statuses.
type SaveStatus string

const (
	SaveStatusSaved    SaveStatus = "saved"
	SaveStatusConflict SaveStatus = "conflict"
)

// SaveResult is either a saved baseline or a revision conflict. For saved,
// Content must be non-nil and describe the exact saved bytes and new revision;
// ObservedRevision is empty. For conflict, Content is nil and no bytes were
// written. ObservedRevision, when available, identifies the current raw bytes.
// A confirmed overwrite must submit a new request against that observed
// revision and may conflict again; it never bypasses revision checks.
type SaveResult struct {
	Status           SaveStatus `json:"status"`
	Content          *Content   `json:"content,omitempty"`
	ObservedRevision string     `json:"observedRevision,omitempty"`
}

// ErrUnsupportedEditText reports invalid UTF-8, binary text, non-LF editor
// input, or unsupported original newline metadata.
var ErrUnsupportedEditText = errors.New("unsupported edit text")

// ErrEditTooLarge reports serialized file bytes exceeding MaxEditBytes,
// including a preserved BOM and any CRLF expansion.
var ErrEditTooLarge = errors.New("edit text is too large")

// encodeEditText restores the original file format from an LF editor snapshot.
// It neither adds nor removes a final newline, and does not strip a BOM from
// text: the caller excludes only the original leading BOM when opening the
// editor. Newlines introduced into a previously single-line file use LF.
// Eligibility is deliberately checked separately by the future save operation;
// supported format metadata currently carries EditNotChecked.
func encodeEditText(text string, format EditInfo) ([]byte, error) {
	if !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\r") {
		return nil, ErrUnsupportedEditText
	}
	switch format.Newline {
	case NewlineLF, NewlineCRLF, NewlineNone:
	default:
		return nil, ErrUnsupportedEditText
	}

	// Bound before counting expansion, keeping arithmetic and allocation small
	// even if an untrusted caller provides an extremely large editor snapshot.
	if len(text) > MaxEditBytes {
		return nil, ErrEditTooLarge
	}
	size := len(text)
	if format.BOM {
		size += len(utf8BOM)
	}
	if format.Newline == NewlineCRLF {
		size += strings.Count(text, "\n")
	}
	if size > MaxEditBytes {
		return nil, ErrEditTooLarge
	}

	out := make([]byte, 0, size)
	if format.BOM {
		out = append(out, utf8BOM...)
	}
	if format.Newline == NewlineCRLF {
		for i := 0; i < len(text); i++ {
			if text[i] == '\n' {
				out = append(out, '\r')
			}
			out = append(out, text[i])
		}
	} else {
		out = append(out, text...)
	}
	return out, nil
}
