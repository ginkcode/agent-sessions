package codediagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func frameOf(body string) string { return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body) }

func readAll(t *testing.T, in string, lim LSPLimits) ([]string, error) {
	t.Helper()
	lim = lim.withDefaults()
	fr := newFrameReader(iotest.OneByteReader(strings.NewReader(in)), lim.MaxHeaderBytes, lim.MaxHeaderLines, lim.MaxBodyBytes)
	var out []string
	for {
		b, err := fr.readFrame()
		if err != nil {
			return out, err
		}
		out = append(out, string(b))
	}
}

func TestLSPFrameFragmentedMultipleAndUTF8(t *testing.T) {
	bodies := []string{`{"a":"héllo ✓ 😀"}`, `{}`, `{"b":1}`}
	var in string
	for _, b := range bodies {
		in += frameOf(b)
	}
	got, err := readAll(t, in, LSPLimits{})
	if !errors.Is(err, io.EOF) || len(got) != 3 || got[0] != bodies[0] {
		t.Fatalf("got %v %v", got, err)
	}
	// Byte length, not rune count.
	if len(bodies[0]) == len([]rune(bodies[0])) {
		t.Fatal("fixture must be multibyte")
	}
	// Content-Type with utf-8 and case-insensitive names are accepted.
	got, err = readAll(t, "content-length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n{}", LSPLimits{})
	if !errors.Is(err, io.EOF) || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestLSPFrameInvalid(t *testing.T) {
	big := fmt.Sprintf("Content-Length: %d\r\n\r\n", 1<<40)
	cases := map[string]struct {
		in   string
		want error
	}{
		"missing length":   {"X-A: b\r\n\r\n{}", ErrLSPBadLength},
		"duplicate length": {"Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", ErrLSPBadLength},
		"negative":         {"Content-Length: -1\r\n\r\n", ErrLSPBadLength},
		"plus":             {"Content-Length: +2\r\n\r\n{}", ErrLSPBadLength},
		"letters":          {"Content-Length: 1x\r\n\r\n", ErrLSPBadLength},
		"empty":            {"Content-Length:\r\n\r\n", ErrLSPBadLength},
		"huge":             {big, ErrLSPBadLength},
		"over limit":       {"Content-Length: 100\r\n\r\n", ErrLSPFrameTooLarge},
		"no colon":         {"garbage\r\n\r\n", ErrLSPBadHeader},
		"bare LF":          {"Content-Length: 2\n\n{}", ErrLSPBadHeader},
		"binary garbage":   {"\x00\x01\x02\r\n", ErrLSPBadHeader},
		"long header":      {"X: " + strings.Repeat("a", 300) + "\r\n", ErrLSPHeaderLimit},
		"endless header":   {strings.Repeat("a", 5000), ErrLSPHeaderLimit},
		"many headers":     {strings.Repeat("X: a\r\n", 20), ErrLSPHeaderLimit},
		"bad charset":      {"Content-Length: 2\r\nContent-Type: x; charset=latin1\r\n\r\n{}", ErrLSPBadHeader},
		"truncated body":   {"Content-Length: 10\r\n\r\n{}", io.ErrUnexpectedEOF},
		"truncated header": {"Content-Length: 1", io.ErrUnexpectedEOF},
		"eof mid headers":  {"Content-Length: 2\r\n", io.ErrUnexpectedEOF},
	}
	for name, c := range cases {
		lim := LSPLimits{MaxBodyBytes: 64, MaxHeaderBytes: 200}
		_, err := readAll(t, c.in, lim)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", name, err, c.want)
		}
	}
}

func TestLSPFrameDoesNotAllocateAdvertisedBody(t *testing.T) {
	fr := newFrameReader(strings.NewReader("Content-Length: 2147483647\r\n\r\n"), 1024, 8, 1<<20)
	if _, err := fr.readFrame(); !errors.Is(err, ErrLSPFrameTooLarge) {
		t.Fatal(err)
	}
}

// fakeServer wraps the far ends of in-memory pipes.
type fakeServer struct {
	t      *testing.T
	fromTr *io.PipeReader // client stdin as seen by the server
	toTr   *io.PipeWriter // server stdout
	errW   *io.PipeWriter
	fr     *frameReader
}

func (f *fakeServer) send(body string) {
	f.t.Helper()
	if _, err := io.WriteString(f.toTr, frameOf(body)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeServer) recv() map[string]any {
	f.t.Helper()
	b, err := f.fr.readFrame()
	if err != nil {
		f.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

type pipeCloser []io.Closer

func (p pipeCloser) Close() error {
	for _, c := range p {
		_ = c.Close()
	}
	return nil
}

func newFake(t *testing.T, lim LSPLimits) (*LSPTransport, *fakeServer) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	tr := NewLSPTransport("att", "gen1", inW, outR, errR, pipeCloser{inR, inW, outR, outW, errR, errW}, lim)
	t.Cleanup(func() { _ = tr.Close() })
	return tr, &fakeServer{t: t, fromTr: inR, toTr: outW, errW: errW, fr: newFrameReader(inR, 1024, 8, 1<<20)}
}

func TestLSPConcurrentRequestsReversedReplies(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{})
	type res struct {
		n   int
		out string
		err error
	}
	ch := make(chan res, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			r, err := tr.Call(context.Background(), "m", map[string]int{"n": i})
			ch <- res{i, string(r), err}
		}(i)
	}
	a, b := srv.recv(), srv.recv()
	// Reply in reverse order, echoing the param each request carried.
	for _, m := range []map[string]any{b, a} {
		n := m["params"].(map[string]any)["n"].(float64)
		srv.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{"n":%v}}`, m["id"], n))
	}
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil || r.out != fmt.Sprintf(`{"n":%d}`, r.n) {
			t.Fatalf("%+v", r)
		}
	}
	if st := tr.Status(); st.Pending != 0 || st.AttachmentID != "att" || st.Generation != "gen1" {
		t.Fatalf("%+v", st)
	}
}

func TestLSPNotificationsServerRequestsAndErrors(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{MaxErrorMessage: 8})
	srv.send(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"x":1}}`)
	srv.send(`{"jsonrpc":"2.0","id":"s1","method":"workspace/applyEdit","params":{}}`)
	m := <-tr.Incoming()
	if m.Kind != LSPNotification || m.Method != "textDocument/publishDiagnostics" || m.ID != nil {
		t.Fatalf("%+v", m)
	}
	m = <-tr.Incoming()
	if m.Kind != LSPServerRequest || string(m.ID) != `"s1"` {
		t.Fatalf("%+v", m)
	}
	go func() { _ = tr.RespondError(context.Background(), m.ID, -32601, "denied") }()
	r := srv.recv()
	if r["id"] != "s1" || r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("%v", r)
	}
	if err := tr.RespondError(context.Background(), m.ID, -32601, "x"); !errors.Is(err, ErrLSPUnknownRequest) {
		t.Fatalf("double respond: %v", err)
	}
	// Server error with an oversized, control-laden message is sanitized.
	go func() {
		srv.recv()
		srv.send(`{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"bad\u0000thing happened"}}`)
	}()
	_, err := tr.Call(context.Background(), "m", nil)
	var re *LSPResponseError
	if !errors.As(err, &re) || re.Message != "badthing" || re.Code != -1 {
		t.Fatalf("%v", err)
	}
}

func TestLSPCancelTimeoutAndLateResponse(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := tr.Call(ctx, "slow", nil); errc <- err }()
	req := srv.recv()
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c := srv.recv()
	if c["method"] != "$/cancelRequest" || c["params"].(map[string]any)["id"] != req["id"] {
		t.Fatalf("%v", c)
	}
	srv.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":1}`, req["id"])) // late
	// Timeout.
	go func() { _, _ = srv.fr.readFrame(); _, _ = srv.fr.readFrame() }()
	tctx, tcancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer tcancel()
	if _, err := tr.Call(tctx, "slow", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// Default timeout applies without a deadline.
	tr2, srv2 := newFake(t, LSPLimits{RequestTimeout: 20 * time.Millisecond})
	go func() { _, _ = srv2.fr.readFrame(); _, _ = srv2.fr.readFrame() }()
	if _, err := tr2.Call(context.Background(), "slow", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// Transport stays healthy and the late response is counted, not fatal.
	deadline := time.Now().Add(2 * time.Second)
	for tr.Status().Unmatched == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if st := tr.Status(); st.Unmatched != 1 || st.Closed || st.Pending != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestLSPPendingLimit(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{MaxPending: 1})
	go func() { _, _ = tr.Call(context.Background(), "a", nil) }()
	srv.recv()
	if _, err := tr.Call(context.Background(), "b", nil); !errors.Is(err, ErrLSPTooManyPending) {
		t.Fatal(err)
	}
}

func TestLSPIncomingOverflowIsVisibleAndFailsPending(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{MaxIncoming: 2})
	errc := make(chan error, 1)
	go func() { _, err := tr.Call(context.Background(), "a", nil); errc <- err }()
	srv.recv()
	// Nobody consumes Incoming: the third message overflows.
	go func() {
		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(srv.toTr, frameOf(`{"jsonrpc":"2.0","method":"n"}`))
		}
	}()
	if err := <-errc; !errors.Is(err, ErrLSPQueueFull) {
		t.Fatal(err)
	}
	<-tr.Done()
	if !errors.Is(tr.Err(), ErrLSPQueueFull) || !tr.Status().Closed {
		t.Fatal(tr.Err())
	}
	if _, err := tr.Call(context.Background(), "x", nil); !errors.Is(err, ErrLSPQueueFull) {
		t.Fatal(err)
	}
}

func TestLSPProtocolFailureFailsPendingOnce(t *testing.T) {
	for name, payload := range map[string]string{
		"bad json":     frameOf(`{nope`),
		"bad framing":  "Content-Length: x\r\n\r\n",
		"not jsonrpc":  frameOf(`{"id":1,"result":1}`),
		"both":         frameOf(`{"jsonrpc":"2.0","id":1,"result":1,"error":{"code":1,"message":"x"}}`),
		"null id req":  frameOf(`{"jsonrpc":"2.0","id":null,"method":"m"}`),
		"empty object": frameOf(`{"jsonrpc":"2.0"}`),
	} {
		t.Run(name, func(t *testing.T) {
			tr, srv := newFake(t, LSPLimits{})
			errc := make(chan error, 1)
			go func() { _, err := tr.Call(context.Background(), "a", nil); errc <- err }()
			srv.recv()
			_, _ = io.WriteString(srv.toTr, payload)
			err := <-errc
			if err == nil || errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !errors.Is(tr.Err(), err) {
				t.Fatalf("%v vs %v", tr.Err(), err)
			}
		})
	}
}

func TestLSPEOFReleasesEverything(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{})
	errc := make(chan error, 1)
	go func() { _, err := tr.Call(context.Background(), "a", nil); errc <- err }()
	srv.recv()
	srv.send(`{"jsonrpc":"2.0","method":"queued"}`)
	_ = srv.toTr.Close()
	if err := <-errc; !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	// Queued message still drains, then the channel closes.
	if m, ok := <-tr.Incoming(); !ok || m.Method != "queued" {
		t.Fatalf("%+v %v", m, ok)
	}
	if _, ok := <-tr.Incoming(); ok {
		t.Fatal("incoming not closed")
	}
	if err := tr.Notify(context.Background(), "x", nil); err == nil {
		t.Fatal("notify after EOF")
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLSPCloseUnblocksBlockedWriterAndWaiters(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{})
	_ = srv // never reads stdin: writes block on the pipe
	errc := make(chan error, 2)
	go func() { _, err := tr.Call(context.Background(), "a", nil); errc <- err }()
	go func() { errc <- tr.Notify(context.Background(), "b", nil) }()
	time.Sleep(20 * time.Millisecond)
	closed := make(chan struct{})
	go func() { _ = tr.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung")
	}
	for i := 0; i < 2; i++ {
		if err := <-errc; err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestLSPOutgoingQueueAndBodyLimits(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{MaxOutgoing: 1, MaxBodyBytes: 256})
	_ = srv // writer blocks; queue fills
	big := strings.Repeat("x", 1024)
	if _, err := tr.Call(context.Background(), "m", big); !errors.Is(err, ErrLSPFrameTooLarge) {
		t.Fatal(err)
	}
	if st := tr.Status(); st.Pending != 0 {
		t.Fatal("pending leaked")
	}
	var full bool
	for i := 0; i < 5 && !full; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err := tr.Call(ctx, "m", nil)
		cancel()
		full = errors.Is(err, ErrLSPQueueFull)
	}
	if !full {
		t.Fatal("outgoing queue never reported full")
	}
}

type shortWriter struct{ buf bytes.Buffer }

func (s *shortWriter) Write(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return s.buf.Write(p)
}

func TestLSPShortWritesAndFrameIntegrity(t *testing.T) {
	var w shortWriter
	if err := writeFull(&w, []byte("abcdefgh")); err != nil || w.buf.String() != "abcdefgh" {
		t.Fatal(err)
	}
	if err := writeFull(zeroWriter{}, []byte("a")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	// Concurrent writers never interleave frames.
	tr, srv := newFake(t, LSPLimits{})
	const n = 20
	for i := 0; i < n; i++ {
		go func(i int) { _ = tr.Notify(context.Background(), "n", map[string]int{"i": i}) }(i)
	}
	seen := map[float64]bool{}
	for i := 0; i < n; i++ {
		seen[srv.recv()["params"].(map[string]any)["i"].(float64)] = true
	}
	if len(seen) != n {
		t.Fatal(seen)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestLSPStderrBoundedSanitizedAndSeparate(t *testing.T) {
	tr, srv := newFake(t, LSPLimits{MaxStderrBytes: 16})
	go func() {
		_, _ = io.WriteString(srv.errW, "Content-Length: 5\r\n\r\nhello\x00\x1b[31m "+strings.Repeat("é", 100))
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !tr.Status().StderrTruncated && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	st := tr.Status()
	if len(st.Stderr) > 16 || !st.StderrTruncated || strings.ContainsAny(st.Stderr, "\x00\x1b\r") || st.Closed {
		t.Fatalf("%+v", st)
	}
	// stdout still frames normally.
	srv.send(`{"jsonrpc":"2.0","method":"ok"}`)
	if m := <-tr.Incoming(); m.Method != "ok" {
		t.Fatal(m)
	}
}
