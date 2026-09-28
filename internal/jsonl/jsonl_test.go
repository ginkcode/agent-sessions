package jsonl

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustReader(t *testing.T, content string, offset int64) *Reader {
	t.Helper()
	r := NewReader(strings.NewReader(content), offset)
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func collect(t *testing.T, r *Reader) ([]string, []int64, error) {
	t.Helper()
	var lines []string
	var starts []int64
	for {
		line, start, err := r.Next()
		if errors.Is(err, io.EOF) {
			return lines, starts, nil
		}
		if err != nil {
			return lines, starts, err
		}
		lines = append(lines, string(line))
		starts = append(starts, start)
	}
}

func TestNextBasic(t *testing.T) {
	r := mustReader(t, "a\nbb\nccc\n", 0)
	lines, starts, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := []string{"a", "bb", "ccc"}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
	if r.LineNo() != 3 {
		t.Errorf("LineNo = %d, want 3", r.LineNo())
	}
	if r.Offset() != int64(len("a\nbb\nccc\n")) {
		t.Errorf("Offset = %d", r.Offset())
	}
	wantStarts := []int64{0, 2, 5}
	for i := range wantStarts {
		if starts[i] != wantStarts[i] {
			t.Errorf("start %d = %d, want %d", i, starts[i], wantStarts[i])
		}
	}
}

func TestCRLFAndEmptyLines(t *testing.T) {
	r := mustReader(t, "one\r\n\r\n\ntwo\r\n\n", 0)
	lines, starts, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("got %q, want [one two]", lines)
	}
	if r.LineNo() != 5 {
		t.Errorf("LineNo = %d, want 5 physical lines", r.LineNo())
	}
	if starts[0] != 0 {
		t.Errorf("starts[0] = %d, want 0", starts[0])
	}
	// "one\r\n" is 5 bytes, "\r\n" is 2 bytes, "\n" is 1 byte -> start of "two\r\n" is 8
	if starts[1] != 8 {
		t.Errorf("starts[1] = %d, want 8", starts[1])
	}
	if r.Offset() != int64(len("one\r\n\r\n\ntwo\r\n\n")) {
		t.Errorf("Offset = %d", r.Offset())
	}
}

func TestNoTrailingNewlineIsHeld(t *testing.T) {
	r := mustReader(t, "first\npartial-tail", 0)
	lines, _, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 1 || lines[0] != "first" {
		t.Fatalf("lines = %q", lines)
	}
	if got := r.Offset(); got != int64(len("first\n")) {
		t.Errorf("Offset = %d, want %d (tail excluded)", got, len("first\n"))
	}
	tail := r.Trailing()
	if string(tail) != "partial-tail" {
		t.Errorf("Trailing = %q, want partial-tail", tail)
	}
}

func TestEmptyFile(t *testing.T) {
	r := mustReader(t, "", 0)
	if _, _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
	if r.Trailing() != nil {
		t.Errorf("Trailing = %q, want nil", r.Trailing())
	}
	if r.Offset() != 0 || r.LineNo() != 0 {
		t.Errorf("Offset = %d, LineNo = %d", r.Offset(), r.LineNo())
	}
}

func TestOnlyNewlines(t *testing.T) {
	r := mustReader(t, "\n\n\n", 0)
	lines, _, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("lines = %q, want none", lines)
	}
	if r.Offset() != 3 || r.LineNo() != 3 {
		t.Errorf("Offset = %d, LineNo = %d", r.Offset(), r.LineNo())
	}
}

func TestResumeAtOffset(t *testing.T) {
	content := "aaa\nbbb\nccc\n"
	// Simulate resume from offset 4 (start of "bbb\n")
	r := mustReader(t, content[4:], 4)
	lines, starts, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 2 || lines[0] != "bbb" || lines[1] != "ccc" {
		t.Fatalf("lines = %q, want [bbb ccc]", lines)
	}
	if starts[0] != 4 {
		t.Errorf("start = %d, want 4", starts[0])
	}
	if starts[1] != 8 {
		t.Errorf("start = %d, want 8", starts[1])
	}
	if r.Offset() != int64(len(content)) {
		t.Errorf("Offset = %d, want %d", r.Offset(), len(content))
	}
}

func TestOpenResumeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	content := "l1\nl2\nl3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	line, start, err := r.Next()
	if err != nil || string(line) != "l1" || start != 0 {
		t.Fatalf("Next: %q at %d, err %v", line, start, err)
	}
	off := r.Offset()
	if off != int64(len("l1\n")) {
		t.Fatalf("Offset = %d, want %d", off, len("l1\n"))
	}
	if err = r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Resume from off
	r2, err := Open(path, off)
	if err != nil {
		t.Fatalf("Open(resume): %v", err)
	}
	defer func() { _ = r2.Close() }()
	lines, starts, err := collect(t, r2)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 2 || lines[0] != "l2" || lines[1] != "l3" {
		t.Fatalf("lines = %q, want [l2 l3]", lines)
	}
	if starts[0] != off {
		t.Errorf("starts[0] = %d, want %d", starts[0], off)
	}
}

func TestOpenMissingOrInvalid(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nope.jsonl"), 0); err == nil {
		t.Fatal("Open on missing file should fail")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "nope.jsonl"), -1); err == nil {
		t.Fatal("Open with negative offset should fail")
	}
}

func TestLineAroundBufferBoundary(t *testing.T) {
	// A line that straddles the 1 MiB read buffer must still be returned whole.
	head := strings.Repeat("a", bufferSize-5)
	content := "s\n" + head + "\ntail\n"
	r := mustReader(t, content, 0)
	lines, starts, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[1] != head {
		t.Errorf("boundary line len = %d, want %d", len(lines[1]), len(head))
	}
	if lines[2] != "tail" {
		t.Errorf("line after boundary = %q", lines[2])
	}
	if starts[1] != 2 {
		t.Errorf("start = %d, want 2", starts[1])
	}
	if r.Offset() != int64(len(content)) {
		t.Errorf("Offset = %d, want %d", r.Offset(), len(content))
	}
}

func Test20MiBLine(t *testing.T) {
	const size = 20 << 20 // 20 MiB
	data := make([]byte, size+1)
	for i := 0; i < size; i++ {
		data[i] = 'a' + byte(i%26)
	}
	data[size] = '\n'
	content := append([]byte("start\n"), data...)
	content = append(content, []byte("end\n")...)

	r := NewReader(bytes.NewReader(content), 0)
	lines, starts, err := collect(t, r)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "start" || lines[2] != "end" {
		t.Fatalf("lines[0]=%q, lines[2]=%q", lines[0], lines[2])
	}
	if len(lines[1]) != size {
		t.Fatalf("20 MiB line len = %d, want %d", len(lines[1]), size)
	}
	if starts[1] != int64(len("start\n")) {
		t.Errorf("start[1] = %d, want %d", starts[1], len("start\n"))
	}
	if r.Offset() != int64(len(content)) {
		t.Errorf("Offset = %d, want %d", r.Offset(), len(content))
	}
}

type stepReader struct {
	chunks [][]byte
	idx    int
}

func (s *stepReader) Read(p []byte) (int, error) {
	if s.idx >= len(s.chunks) {
		return 0, io.EOF
	}
	chunk := s.chunks[s.idx]
	s.idx++
	n := copy(p, chunk)
	return n, nil
}

func TestPartialTailRetryOnGrowingSource(t *testing.T) {
	sr := &stepReader{chunks: [][]byte{[]byte("one\npar")}}
	r := NewReader(sr, 0)

	line, start, err := r.Next()
	if err != nil || string(line) != "one" || start != 0 {
		t.Fatalf("Next: line=%q start=%d err=%v", line, start, err)
	}

	line, _, err = r.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v (line=%q)", err, line)
	}
	if tail := string(r.Trailing()); tail != "par" {
		t.Fatalf("Trailing = %q, want 'par'", tail)
	}
	if r.Offset() != int64(len("one\n")) {
		t.Fatalf("Offset = %d, want %d (tail excluded)", r.Offset(), len("one\n"))
	}

	// Source grows: append remaining piece ending with newline
	sr.chunks = append(sr.chunks, []byte("tial\n"))

	line, start, err = r.Next()
	if err != nil {
		t.Fatalf("retry Next: %v", err)
	}
	if string(line) != "partial" {
		t.Fatalf("retry Next line = %q, want 'partial'", line)
	}
	if start != int64(len("one\n")) {
		t.Errorf("start = %d, want %d", start, len("one\n"))
	}
	if r.Offset() != int64(len("one\npartial\n")) {
		t.Errorf("Offset = %d, want %d", r.Offset(), len("one\npartial\n"))
	}
	if r.Trailing() != nil {
		t.Errorf("Trailing after complete line = %q, want nil", r.Trailing())
	}
}

type repeatByteReader struct {
	b     byte
	count int64
	read  int64
}

func (r *repeatByteReader) Read(p []byte) (int, error) {
	if r.read >= r.count {
		return 0, io.EOF
	}
	rem := r.count - r.read
	toRead := int64(len(p))
	if toRead > rem {
		toRead = rem
	}
	for i := int64(0); i < toRead; i++ {
		p[i] = r.b
	}
	r.read += toRead
	return int(toRead), nil
}

func TestLineTooLongRecoverable(t *testing.T) {
	repeater := &repeatByteReader{b: 'x', count: int64(MaxLineLen + 1024)}
	src := io.MultiReader(
		strings.NewReader("ok\n"),
		repeater,
		strings.NewReader("\nafter\n"),
	)
	r := NewReader(src, 0)

	line, _, err := r.Next()
	if err != nil || string(line) != "ok" {
		t.Fatalf("first line: %q, %v", line, err)
	}

	_, _, err = r.Next()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("expected ErrLineTooLong, got %v", err)
	}

	line, _, err = r.Next()
	if err != nil || string(line) != "after" {
		t.Fatalf("after line: %q, %v", line, err)
	}
}

func TestLineTooLongNoFinalNewline(t *testing.T) {
	repeater := &repeatByteReader{b: 'z', count: int64(MaxLineLen + 1024)}
	src := io.MultiReader(
		strings.NewReader("ok\n"),
		repeater,
	)
	r := NewReader(src, 0)

	line, _, err := r.Next()
	if err != nil || string(line) != "ok" {
		t.Fatalf("first line: %q, %v", line, err)
	}

	_, _, err = r.Next()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("expected ErrLineTooLong, got %v", err)
	}

	if r.Trailing() != nil {
		t.Errorf("Trailing after oversized discarded line = %q, want nil", r.Trailing())
	}
}

func TestNestedTypeBeforeTopLevel(t *testing.T) {
	// Claude assistant records put a nested "type" inside "message" before
	// the top-level "type"; naive search would find the wrong one.
	line := []byte(`{"parentUuid":"x","message":{"id":"m1","role":"assistant","type":"message","model":"claude"},"type":"assistant","timestamp":"2026-01-01T00:00:00Z"}`)
	if got := Type(line); got != "assistant" {
		t.Errorf("Type = %q, want assistant", got)
	}
	nested := []byte(`{"message":{"content":[{"type":"text"}]},"type":"user"}`)
	if got := Type(nested); got != "user" {
		t.Errorf("Type = %q, want user", got)
	}
	if got := Type([]byte(`not json`)); got != "" {
		t.Errorf("Type on invalid JSON = %q, want empty", got)
	}
	if got := Type(nil); got != "" {
		t.Errorf("Type(nil) = %q", got)
	}
}

func TestCallerCopySemantics(t *testing.T) {
	// A line spanning buffer fills uses r.scratch, which is reused across
	// calls. A caller retaining a line must copy it before calling Next again.
	prefix := strings.Repeat("x", bufferSize)
	content := prefix + "1\n" + prefix + "2\n"
	r := mustReader(t, content, 0)
	first, _, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	kept := append([]byte(nil), first...)
	_, _, err = r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, []byte(prefix+"1")) {
		t.Fatal("copied line changed after Next")
	}
}

func BenchmarkScan38MB(b *testing.B) {
	lineTemplate := `{"type":"assistant","message":{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"text","text":"hello world"}]},"timestamp":"2026-09-28T12:00:00Z"}` + "\n"
	repeat := (38 << 20) / len(lineTemplate)
	var buf bytes.Buffer
	buf.Grow(repeat * len(lineTemplate))
	for i := 0; i < repeat; i++ {
		fmt.Fprintf(&buf, `{"type":"assistant","message":{"id":"msg_%06d","type":"message","role":"assistant","content":[{"type":"text","text":"chunk %d"}]},"timestamp":"2026-09-28T12:00:00Z"}`+"\n", i, i)
	}
	data := buf.Bytes()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := NewReader(bytes.NewReader(data), 0)
		count := 0
		for {
			line, _, err := r.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				b.Fatalf("Next: %v", err)
			}
			if typ := Type(line); typ != "assistant" {
				b.Fatalf("unexpected type: %s", typ)
			}
			count++
		}
		if count != repeat {
			b.Fatalf("got %d lines, want %d", count, repeat)
		}
	}
}
