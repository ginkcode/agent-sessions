package codediagnostics

// LSP stdio framing (CD-18). This is deliberately independent of the app RPC
// newline framing in internal/rpc: LSP uses "Content-Length: N\r\n\r\n" headers
// followed by exactly N bytes of UTF-8 JSON.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Protocol failures. All are terminal for a transport; none carries frame bytes.
var (
	ErrLSPBadHeader     = errors.New("lsp: malformed frame header")
	ErrLSPHeaderLimit   = errors.New("lsp: frame header exceeds limit")
	ErrLSPBadLength     = errors.New("lsp: missing or invalid Content-Length")
	ErrLSPFrameTooLarge = errors.New("lsp: frame body exceeds limit")
	ErrLSPBadJSON       = errors.New("lsp: invalid JSON-RPC message")
)

// frameReader incrementally parses Content-Length framed bodies.
type frameReader struct {
	r                              *bufio.Reader
	maxHeaderBytes, maxHeaderLines int
	maxBody                        int
}

func newFrameReader(r io.Reader, maxHeaderBytes, maxHeaderLines, maxBody int) *frameReader {
	return &frameReader{r: bufio.NewReaderSize(r, 4096), maxHeaderBytes: maxHeaderBytes, maxHeaderLines: maxHeaderLines, maxBody: maxBody}
}

// readFrame returns io.EOF only at a clean frame boundary; a stream that ends
// inside a header or body yields io.ErrUnexpectedEOF.
func (f *frameReader) readFrame() ([]byte, error) {
	length := -1
	lines, total := 0, 0
	for {
		line, err := f.readHeaderLine(f.maxHeaderBytes - total)
		if err != nil {
			if errors.Is(err, io.EOF) && total > 0 {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		total += len(line) + 2
		if line == "" {
			break
		}
		lines++
		if lines > f.maxHeaderLines {
			return nil, ErrLSPHeaderLimit
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || name == "" || strings.TrimSpace(name) != name {
			return nil, ErrLSPBadHeader
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(name) {
		case "content-length":
			if length >= 0 {
				return nil, fmt.Errorf("%w: duplicate", ErrLSPBadLength)
			}
			n, err := parseLength(value)
			if err != nil {
				return nil, err
			}
			if n > f.maxBody {
				return nil, ErrLSPFrameTooLarge
			}
			length = n
		case "content-type":
			if !utf8Content(value) {
				return nil, ErrLSPBadHeader
			}
		}
	}
	if length < 0 {
		return nil, ErrLSPBadLength
	}
	body := make([]byte, length) // length already validated against the limit
	if _, err := io.ReadFull(f.r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// readHeaderLine reads one CRLF-terminated line, never buffering more than budget bytes.
func (f *frameReader) readHeaderLine(budget int) (string, error) {
	var buf []byte
	for {
		b, err := f.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(buf) > 0 {
				err = io.ErrUnexpectedEOF
			}
			return "", err
		}
		if b == '\n' {
			if len(buf) == 0 || buf[len(buf)-1] != '\r' {
				return "", ErrLSPBadHeader
			}
			return string(buf[:len(buf)-1]), nil
		}
		if len(buf) >= budget-2 || len(buf) >= f.maxHeaderBytes {
			return "", ErrLSPHeaderLimit
		}
		if b == 0 || (b < 0x20 && b != '\r' && b != '\t') || b >= 0x7f {
			return "", ErrLSPBadHeader
		}
		buf = append(buf, b)
	}
}

func parseLength(v string) (int, error) {
	if v == "" || len(v) > 10 {
		return 0, ErrLSPBadLength
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, ErrLSPBadLength
		}
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil || n > 1<<31-1 {
		return 0, ErrLSPBadLength
	}
	return int(n), nil
}

func utf8Content(v string) bool {
	_, params, ok := strings.Cut(strings.ToLower(v), ";")
	if !ok {
		return true
	}
	params = strings.ReplaceAll(strings.TrimSpace(params), " ", "")
	return params == "charset=utf-8" || params == "charset=utf8"
}

// encodeFrame builds one complete frame so it can be written without interleaving.
func encodeFrame(body []byte, max int) ([]byte, error) {
	if len(body) > max {
		return nil, ErrLSPFrameTooLarge
	}
	hdr := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	out := make([]byte, 0, len(hdr)+len(body))
	return append(append(out, hdr...), body...), nil
}

// writeFull handles short writes from non-conforming writers.
func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
