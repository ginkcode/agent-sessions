package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// MaxEditBytes is the largest text file the viewer may offer to edit. It is
// well below MaxReadBytes: an editor keeps the original, the draft and its
// undo history in memory, and a save sends the whole text back over RPC.
// Larger text files are still read, with a revision, but stay read-only.
const MaxEditBytes = 500000

// Reasons a text file cannot be edited, in EditInfo.Reason.
const (
	// EditTooLarge: the file is larger than MaxEditBytes.
	EditTooLarge = "too_large"
	// EditUnsupportedNewlines: the file mixes LF and CRLF line endings or
	// has a bare CR, which the editor could not write back unchanged.
	EditUnsupportedNewlines = "unsupported_newlines"
	// EditNotChecked: the text format is supported, but the read did not
	// check whether the file may be written (see EditPolicy and the
	// service's editing check), so it is not offered for editing.
	EditNotChecked = "not_checked"
	// EditProtected: the file is in a tool's data store, credentials or
	// repository metadata, which the viewer never edits.
	EditProtected = "protected"
	// EditLinked: the path goes through a symlink or another link.
	EditLinked = "linked"
	// EditReadOnly: the file is not writable by this user.
	EditReadOnly = "read_only"
	// EditUnsupported: the host cannot save files.
	EditUnsupported = "unsupported"
)

// Newline styles of a text file, in EditInfo.Newline.
const (
	NewlineLF   = "lf"
	NewlineCRLF = "crlf"
	// NewlineNone: the file has no line breaks at all.
	NewlineNone = "none"
)

// utf8BOM is the byte order mark some editors put at the start of UTF-8 text.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// EditInfo tells whether a text file may be edited, and the format a save
// has to keep: its newline style and byte order mark. Text in Content is
// always the file's bytes unchanged, BOM and CRs included.
type EditInfo struct {
	Eligible bool `json:"eligible"`
	// Reason says why the file is not eligible: see the Edit constants.
	Reason string `json:"reason,omitempty"`
	// Newline is the file's line ending style (see the Newline constants);
	// empty when the line endings are unsupported.
	Newline string `json:"newline,omitempty"`
	// BOM is set when the file starts with a UTF-8 byte order mark.
	BOM bool `json:"bom,omitempty"`
}

// revisionOf returns the opaque revision of a file's contents: the SHA-256
// of its exact bytes. A save compares it to tell whether the file changed
// since it was read.
func revisionOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// textEditInfo describes the text data for editing. data must already be
// known to be text (see IsBinary).
func textEditInfo(data []byte) *EditInfo {
	info := &EditInfo{BOM: bytes.HasPrefix(data, utf8BOM)}
	newline, ok := newlineStyle(data)
	if ok {
		info.Newline = newline
	}
	switch {
	case len(data) > MaxEditBytes:
		info.Reason = EditTooLarge
	case !ok:
		info.Reason = EditUnsupportedNewlines
	default:
		// A format check only; CheckEdit decides whether it may be saved.
		info.Reason = EditNotChecked
	}
	return info
}

// newlineStyle returns the one line ending style data uses. It reports
// false for mixed LF and CRLF endings and for a CR not followed by LF.
func newlineStyle(data []byte) (string, bool) {
	var lf, crlf bool
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\r':
			if i+1 >= len(data) || data[i+1] != '\n' {
				return "", false
			}
			crlf = true
			i++
		case '\n':
			lf = true
		}
		if lf && crlf {
			return "", false
		}
	}
	switch {
	case crlf:
		return NewlineCRLF, true
	case lf:
		return NewlineLF, true
	}
	return NewlineNone, true
}
