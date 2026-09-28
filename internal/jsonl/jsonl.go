// Package jsonl provides a resumable reader for newline-delimited JSON.
package jsonl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tidwall/gjson"
)

// ErrLineTooLong is returned when a line exceeds MaxLineLen. The reader
// discards through the next newline and remains usable for subsequent lines.
var ErrLineTooLong = errors.New("jsonl: line too long")

// MaxLineLen is the maximum number of bytes before a newline (256 MiB).
const MaxLineLen = 256 << 20

const bufferSize = 1 << 20

// Reader tracks the offset of the last complete line in a JSONL stream.
type Reader struct {
	f        *os.File
	br       *bufio.Reader
	off      int64 // end of last complete line, including its newline
	pending  int64 // bytes read in the incomplete current line
	line     int   // physical lines terminated by a newline (including skipped lines)
	scratch  []byte
	trailing bool
	discard  bool // oversized line without a newline yet
	maxLine  int  // defaults to MaxLineLen; overridable in package tests
}

// Open opens a file read-only at offset, which should be an earlier Offset().
func Open(path string, offset int64) (*Reader, error) {
	if offset < 0 {
		return nil, fmt.Errorf("jsonl: negative offset %d", offset)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("jsonl: open %s: %w", path, err)
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("jsonl: seek %s: %w", path, err)
	}
	return &Reader{f: f, br: bufio.NewReaderSize(f, bufferSize), off: offset, maxLine: MaxLineLen}, nil
}

// NewReader wraps an io.Reader already positioned at offset. For an existing
// file, use Open instead to perform the seek. The caller owns the io.Reader.
func NewReader(src io.Reader, offset int64) *Reader {
	return &Reader{br: bufio.NewReaderSize(src, bufferSize), off: offset, maxLine: MaxLineLen}
}

// Next returns the next complete, non-empty line without its newline, plus
// the byte offset where it started. A trailing '\r' from CRLF is also removed.
// Empty lines are skipped. io.EOF means no complete line is available; an
// unterminated tail is accessible via Trailing and is excluded from Offset.
//
// The returned slice is only valid until the next Next call. Copy it if it
// needs to be retained. Next can be retried on a growing source after EOF.
func (r *Reader) Next() ([]byte, int64, error) {
	r.trailing = false
	if r.discard {
		if done, err := r.skipLong(); !done || err != nil {
			return nil, r.off, err
		}
	}

	for {
		start := r.off
		frag, err := r.br.ReadSlice('\n')
		r.pending += int64(len(frag))
		if err == nil {
			r.line++
			// frag includes the newline; size is checked before accumulating
			// into scratch to avoid allocating for oversized records.
			if len(r.scratch)+len(frag)-1 > r.maxLine {
				r.advance()
				return nil, start, ErrLineTooLong
			}
			if len(r.scratch) != 0 {
				r.scratch = append(r.scratch, frag[:len(frag)-1]...)
				frag = r.scratch
			} else {
				frag = frag[:len(frag)-1]
			}
			r.advance()
			if n := len(frag); n != 0 && frag[n-1] == '\r' {
				frag = frag[:n-1]
			}
			if len(frag) == 0 {
				continue
			}
			return frag, start, nil
		}

		if len(r.scratch)+len(frag) > r.maxLine {
			r.scratch = r.scratch[:0]
			r.discard = true
			_, skipErr := r.skipLong()
			if skipErr != nil && !errors.Is(skipErr, io.EOF) {
				return nil, start, skipErr
			}
			return nil, start, ErrLineTooLong
		}
		r.scratch = append(r.scratch, frag...)
		if err == io.EOF {
			r.trailing = len(r.scratch) != 0
			return nil, start, io.EOF
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, start, fmt.Errorf("jsonl: read: %w", err)
		}
	}
}

// skipLong discards through the newline after an oversized line. If no
// newline is available yet, it retains only the byte count, not its content.
func (r *Reader) skipLong() (bool, error) {
	for {
		frag, err := r.br.ReadSlice('\n')
		r.pending += int64(len(frag))
		if err == nil {
			r.line++
			r.discard = false
			r.advance()
			return true, nil
		}
		if err == io.EOF {
			return false, io.EOF
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return false, fmt.Errorf("jsonl: read: %w", err)
		}
	}
}

func (r *Reader) advance() {
	r.off += r.pending
	r.pending = 0
	r.scratch = r.scratch[:0]
}

// Trailing returns an incomplete, unterminated final line after Next reached
// EOF. A line over MaxLineLen is discarded, so Trailing is nil for that case.
// The returned slice is owned by Reader; copy it if it needs to be retained.
func (r *Reader) Trailing() []byte {
	if !r.trailing {
		return nil
	}
	return r.scratch
}

// Offset is the byte offset just past the last complete line, including
// skipped empty or oversized lines. It excludes an incomplete trailing line.
func (r *Reader) Offset() int64 { return r.off }

// LineNo is the number of complete, non-empty lines returned by Next since
// this reader was constructed (not the file's absolute line number).
func (r *Reader) LineNo() int { return r.line }

// Close closes files opened by Open. A source passed to NewReader remains
// owned by the caller and is not closed.
func (r *Reader) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// Type extracts the top-level "type" value from a record. gjson.GetBytes
// skips nested fields even when they precede the top-level key. This is field
// extraction only, not validation of the complete JSON record.
func Type(line []byte) string {
	return gjson.GetBytes(line, "type").String()
}
