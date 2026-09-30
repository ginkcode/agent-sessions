package rpc

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// Millions of blank lines must be skipped without growing the stack.
func TestFrameReader_ManyBlankLines(t *testing.T) {
	frame := `{"jsonrpc":"2.0","id":1,"result":{}}`
	r := NewFrameReader(io.MultiReader(
		strings.NewReader(strings.Repeat("\n", 8<<20)),
		strings.NewReader(" \r\n\t\n"+frame+"\n"),
	))
	got, err := r.ReadFrame()
	if err != nil || string(got) != frame {
		t.Fatalf("ReadFrame = %q, %v", got, err)
	}
}

// A second response for one ID must not wedge the read loop, even when the
// caller has stopped listening (as a cancelled call does).
func TestClient_DuplicateResponseDoesNotBlock(t *testing.T) {
	cliR, srvW := io.Pipe()
	c := NewClient(cliR, io.Discard)
	defer c.Close()

	abandoned := make(chan *Response, 1) // nobody reads this
	live := make(chan *Response, 1)
	c.mu.Lock()
	c.pending["1"] = abandoned
	c.pending["2"] = live
	c.mu.Unlock()

	go func() {
		_, _ = io.WriteString(srvW, strings.Repeat(`{"jsonrpc":"2.0","id":1,"result":"x"}`+"\n", 3)+
			`{"jsonrpc":"2.0","id":2,"result":"y"}`+"\n")
	}()
	select {
	case resp := <-live:
		if string(resp.Result) != `"y"` {
			t.Fatalf("live result = %s", resp.Result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read loop blocked on a duplicate response")
	}
}

// Output with no newline must stop at the scan budget, not grow forever.
func TestPreface_LineWithoutNewlineIsCapped(t *testing.T) {
	endless := &repeatReader{b: 'x'}
	_, err := WaitForPreface(context.Background(), endless, "nonce")
	if !errors.Is(err, ErrPrefaceNotFound) {
		t.Fatalf("err = %v, want ErrPrefaceNotFound", err)
	}
	if endless.n > 2*MaxScanCapBytes {
		t.Fatalf("read %d bytes, budget is %d", endless.n, MaxScanCapBytes)
	}
}

// A preface after long (but in-budget) banner lines is still found.
func TestPreface_LongBannerLines(t *testing.T) {
	banner := strings.Repeat("b", 10000) + "\n" + strings.Repeat("c", 20000) + "\n"
	r, err := WaitForPreface(context.Background(), strings.NewReader(banner+FormatPreface("n1")+"\nrest"), "n1")
	if err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "rest" {
		t.Fatalf("after preface: %q", rest)
	}
}

type repeatReader struct {
	b byte
	n int64
}

func (r *repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	r.n += int64(len(p))
	return len(p), nil
}

var _ engine.Backend = (*Client)(nil)
