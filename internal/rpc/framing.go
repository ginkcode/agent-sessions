package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

// MaxFrameBytes is the upper limit for a single JSON-RPC message frame (64 MiB).
const MaxFrameBytes = 64 << 20

// MaxScanCapBytes is the preface scanner limit (64 KiB).
const MaxScanCapBytes = 64 << 10

// FrameReader reads newline-delimited JSON frames up to MaxFrameBytes without using
// bufio.Scanner (which defaults to a 64 KiB buffer).
type FrameReader struct {
	reader *bufio.Reader
	limit  int64
}

// NewFrameReader wraps an io.Reader to parse frames with a maximum size limit.
func NewFrameReader(r io.Reader) *FrameReader {
	return NewFrameReaderWithLimit(r, MaxFrameBytes)
}

// NewFrameReaderWithLimit wraps an io.Reader with an explicit frame size limit.
func NewFrameReaderWithLimit(r io.Reader, limit int64) *FrameReader {
	return &FrameReader{
		reader: bufio.NewReaderSize(r, 64*1024),
		limit:  limit,
	}
}

// ReadFrame reads the next newline-delimited JSON frame.
// Returns io.EOF when the input stream closes.
func (r *FrameReader) ReadFrame() ([]byte, error) {
	var buf bytes.Buffer
	for {
		chunk, isPrefix, err := r.reader.ReadLine()
		if err != nil {
			if err == io.EOF && buf.Len() > 0 {
				return buf.Bytes(), nil
			}
			return nil, err
		}

		if int64(buf.Len()+len(chunk)) > r.limit {
			// Discard the remainder of this oversized line before returning error
			for isPrefix && err == nil {
				_, isPrefix, err = r.reader.ReadLine()
			}
			return nil, ErrPayloadTooLarge
		}

		buf.Write(chunk)
		if !isPrefix {
			break
		}
	}

	line := bytes.TrimSpace(buf.Bytes())
	if len(line) == 0 {
		// Empty line (e.g. keep-alive or blank line); read next frame
		return r.ReadFrame()
	}

	return line, nil
}

// FrameWriter writes newline-delimited JSON frames concurrently safely.
type FrameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewFrameWriter wraps an io.Writer with mutex synchronization.
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{writer: w}
}

// WriteFrame serializes payload as JSON and writes it followed by a newline.
func (w *FrameWriter) WriteFrame(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxFrameBytes {
		return ErrPayloadTooLarge
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	data = append(data, '\n')
	if _, err := w.writer.Write(data); err != nil {
		return err
	}
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// WriteRaw writes already-serialized JSON bytes with a newline.
func (w *FrameWriter) WriteRaw(data []byte) error {
	if int64(len(data)) > MaxFrameBytes {
		return ErrPayloadTooLarge
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	if _, err := w.writer.Write(data); err != nil {
		return err
	}
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}
