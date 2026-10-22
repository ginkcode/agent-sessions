package codediagnostics

import (
	"bytes"
	"io"
	"sync"
)

// Capture accounts raw bytes across both streams, including discarded bytes of
// an oversized line. Per-stream line limits bound parser messages before parsing.
// Writers never block on consumers and discard after the first exceeded limit.
type boundedCapture struct {
	mu                 sync.Mutex
	stdout, stderr     bytes.Buffer
	remaining, message int
	line               [2]int
	truncated          bool
	limit              chan struct{}
}

func newCapture(total, message int) *boundedCapture {
	return &boundedCapture{remaining: total, message: message, limit: make(chan struct{})}
}

type captureWriter struct {
	c      *boundedCapture
	stream int
}

func (c *boundedCapture) writer(stderr bool) io.Writer {
	stream := 0
	if stderr {
		stream = 1
	}
	return captureWriter{c, stream}
}

func (w captureWriter) Write(p []byte) (int, error) {
	c := w.c
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	for _, b := range p {
		if c.truncated {
			break
		}
		if c.remaining == 0 || (b != '\n' && c.line[w.stream] >= c.message) {
			c.truncated = true
			close(c.limit)
			break
		}
		c.remaining--
		if b == '\n' {
			c.line[w.stream] = 0
		} else {
			c.line[w.stream]++
		}
		if w.stream == 0 {
			_ = c.stdout.WriteByte(b)
		} else {
			_ = c.stderr.WriteByte(b)
		}
	}
	return n, nil
}

func (c *boundedCapture) result() ([]byte, []byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Clone(c.stdout.Bytes()), bytes.Clone(c.stderr.Bytes()), c.truncated
}
