package codediagnostics

// Bounded LSP stdio JSON-RPC transport (CD-18).
//
// The transport only frames, correlates and queues messages. It never starts a
// process, authorizes a project, executes commands, applies edits or answers
// server requests on its own: server requests are delivered to the owner (CD-19)
// through Incoming and must be answered explicitly. Raw frames and document
// bodies are never logged or retained.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Transport errors.
var (
	ErrLSPClosed         = errors.New("lsp: transport closed")
	ErrLSPTooManyPending = errors.New("lsp: too many pending requests")
	ErrLSPQueueFull      = errors.New("lsp: queue full")
	ErrLSPUnknownRequest = errors.New("lsp: no such server request")
)

// LSPLimits bounds every resource the transport can consume. Zero fields take
// defaults. These are separate from the app RPC frame limit.
type LSPLimits struct {
	MaxHeaderBytes  int           // total header bytes per frame
	MaxHeaderLines  int           // header lines per frame
	MaxBodyBytes    int           // body bytes per frame, inbound and outbound
	MaxPending      int           // concurrent outstanding client requests
	MaxIncoming     int           // queued notifications + server requests
	MaxOutgoing     int           // queued outbound frames
	MaxStderrBytes  int           // retained sanitized stderr bytes
	MaxErrorMessage int           // retained bytes of a response error message
	RequestTimeout  time.Duration // default deadline for Call
}

func (l LSPLimits) withDefaults() LSPLimits {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&l.MaxHeaderBytes, 1<<10)
	def(&l.MaxHeaderLines, 8)
	def(&l.MaxBodyBytes, 8<<20)
	def(&l.MaxPending, 64)
	def(&l.MaxIncoming, 256)
	def(&l.MaxOutgoing, 64)
	def(&l.MaxStderrBytes, 8<<10)
	def(&l.MaxErrorMessage, 512)
	if l.RequestTimeout <= 0 {
		l.RequestTimeout = 30 * time.Second
	}
	return l
}

// LSPKind classifies an inbound server message.
type LSPKind int

const (
	LSPNotification  LSPKind = iota + 1 // method, no id
	LSPServerRequest                    // method and id: needs an explicit response
)

// LSPMessage is an inbound notification or server request.
type LSPMessage struct {
	Kind   LSPKind
	Method string
	ID     json.RawMessage // server request only
	Params json.RawMessage
}

// LSPResponseError is a JSON-RPC error answer from the server. Message is
// sanitized and truncated.
type LSPResponseError struct {
	Code    int
	Message string
}

func (e *LSPResponseError) Error() string {
	return "lsp: server error " + strconv.Itoa(e.Code) + ": " + e.Message
}

// LSPStatus is a bounded snapshot for the manager. Overflow and protocol
// failure are visible here and in Err; nothing is dropped silently.
type LSPStatus struct {
	AttachmentID, Generation string
	Pending, Incoming        int
	Unmatched                int // responses with no waiter (canceled, late or unknown)
	Closed                   bool
	Err                      error
	Stderr                   string
	StderrTruncated          bool
}

type lspResult struct {
	result json.RawMessage
	err    error
}

type lspWrite struct {
	frame []byte
	done  chan error // buffered; nil for fire-and-forget
}

// LSPTransport owns one stdio stream pair. Identity is carried so the manager
// can invalidate a dead stream's attachment/generation.
type LSPTransport struct {
	id     struct{ attachment, generation string }
	limits LSPLimits
	closer io.Closer
	stdin  io.Writer

	incoming chan LSPMessage
	out      chan lspWrite
	done     chan struct{}
	wg       sync.WaitGroup

	mu         sync.Mutex
	nextID     int64
	pending    map[string]chan lspResult
	srvPending map[string]struct{}
	unmatched  int
	err        error
	stderr     bytes.Buffer
	stderrCut  bool
	failOnce   sync.Once
}

// NewLSPTransport starts reader/writer goroutines. closer must unblock reads on
// stdout and writes on stdin (typically closing the process pipes); it is
// closed on failure or Close. stderr may be nil. Ownership of the streams moves
// to the transport.
func NewLSPTransport(attachmentID, generation string, stdin io.Writer, stdout, stderr io.Reader, closer io.Closer, limits LSPLimits) *LSPTransport {
	limits = limits.withDefaults()
	t := &LSPTransport{
		limits: limits, closer: closer, stdin: stdin,
		incoming:   make(chan LSPMessage, limits.MaxIncoming),
		out:        make(chan lspWrite, limits.MaxOutgoing),
		done:       make(chan struct{}),
		pending:    map[string]chan lspResult{},
		srvPending: map[string]struct{}{},
	}
	t.id.attachment, t.id.generation = attachmentID, generation
	t.wg.Add(2)
	go t.readLoop(newFrameReader(stdout, limits.MaxHeaderBytes, limits.MaxHeaderLines, limits.MaxBodyBytes))
	go t.writeLoop()
	if stderr != nil {
		t.wg.Add(1)
		go t.stderrLoop(stderr)
	}
	return t
}

// Incoming delivers notifications and server requests in arrival order. It is
// closed after the transport fails or closes, once queued messages are drained.
func (t *LSPTransport) Incoming() <-chan LSPMessage { return t.incoming }

// Done is closed when the transport has failed or been closed.
func (t *LSPTransport) Done() <-chan struct{} { return t.done }

// Err returns the first failure (ErrLSPClosed after a plain Close), or nil.
func (t *LSPTransport) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Status returns a bounded snapshot.
func (t *LSPTransport) Status() LSPStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return LSPStatus{
		AttachmentID: t.id.attachment, Generation: t.id.generation,
		Pending: len(t.pending), Incoming: len(t.incoming), Unmatched: t.unmatched,
		Closed: t.err != nil, Err: t.err,
		Stderr: t.stderr.String(), StderrTruncated: t.stderrCut,
	}
}

// Close fails pending work with ErrLSPClosed, closes the streams and waits for
// all transport goroutines.
func (t *LSPTransport) Close() error {
	t.fail(ErrLSPClosed)
	t.wg.Wait()
	return nil
}

func (t *LSPTransport) fail(err error) {
	t.failOnce.Do(func() {
		t.mu.Lock()
		t.err = err
		pend := t.pending
		t.pending = map[string]chan lspResult{}
		t.srvPending = map[string]struct{}{}
		t.mu.Unlock()
		for _, ch := range pend {
			ch <- lspResult{err: err} // buffered(1), single sender
		}
		close(t.done)
		if t.closer != nil {
			_ = t.closer.Close()
		}
	})
}

// Call sends a request and waits for its response, ctx cancellation, the
// default timeout or transport failure. Cancellation removes the waiter and
// sends a best-effort $/cancelRequest; a late response is counted and ignored.
func (t *LSPTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.limits.RequestTimeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rawParams, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.err != nil {
		err := t.err
		t.mu.Unlock()
		return nil, err
	}
	if len(t.pending) >= t.limits.MaxPending {
		t.mu.Unlock()
		return nil, ErrLSPTooManyPending
	}
	t.nextID++
	key := strconv.FormatInt(t.nextID, 10)
	ch := make(chan lspResult, 1)
	t.pending[key] = ch
	t.mu.Unlock()

	frame, err := encodeMessage(map[string]any{"jsonrpc": "2.0", "id": json.Number(key), "method": method, "params": rawParams}, t.limits.MaxBodyBytes)
	if err == nil {
		err = t.enqueue(lspWrite{frame: frame})
	}
	if err != nil {
		t.dropPending(key)
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		if t.dropPending(key) {
			t.sendCancel(key)
			return nil, ctx.Err()
		}
		// Response or failure won the race; prefer it.
		r := <-ch
		return r.result, r.err
	}
}

func (t *LSPTransport) dropPending(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.pending[key]; !ok {
		return false
	}
	delete(t.pending, key)
	return true
}

func (t *LSPTransport) sendCancel(key string) {
	frame, err := encodeMessage(map[string]any{"jsonrpc": "2.0", "method": "$/cancelRequest", "params": map[string]json.Number{"id": json.Number(key)}}, t.limits.MaxBodyBytes)
	if err == nil {
		_ = t.enqueue(lspWrite{frame: frame}) // best effort; a full queue must not block cancel
	}
}

// Notify sends a notification. It returns once the frame is fully written,
// ctx ends, or the transport fails.
func (t *LSPTransport) Notify(ctx context.Context, method string, params any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	frame, err := encodeMessage(map[string]any{"jsonrpc": "2.0", "method": method, "params": rawParams}, t.limits.MaxBodyBytes)
	if err != nil {
		return err
	}
	return t.sendWait(ctx, frame)
}

// Respond answers a server request with a result.
func (t *LSPTransport) Respond(ctx context.Context, id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return t.respond(ctx, id, map[string]any{"jsonrpc": "2.0", "id": id, "result": json.RawMessage(raw)})
}

// RespondError answers a server request with a JSON-RPC error (for example
// MethodNotFound, -32601, for denied requests).
func (t *LSPTransport) RespondError(ctx context.Context, id json.RawMessage, code int, message string) error {
	return t.respond(ctx, id, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (t *LSPTransport) respond(ctx context.Context, id json.RawMessage, msg map[string]any) error {
	key := string(id)
	t.mu.Lock()
	if t.err != nil {
		err := t.err
		t.mu.Unlock()
		return err
	}
	if _, ok := t.srvPending[key]; !ok {
		t.mu.Unlock()
		return ErrLSPUnknownRequest
	}
	delete(t.srvPending, key)
	t.mu.Unlock()
	frame, err := encodeMessage(msg, t.limits.MaxBodyBytes)
	if err != nil {
		return err
	}
	return t.sendWait(ctx, frame)
}

func (t *LSPTransport) sendWait(ctx context.Context, frame []byte) error {
	w := lspWrite{frame: frame, done: make(chan error, 1)}
	if err := t.enqueue(w); err != nil {
		return err
	}
	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return t.Err()
	}
}

func (t *LSPTransport) enqueue(w lspWrite) error {
	t.mu.Lock()
	err := t.err
	t.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case t.out <- w:
		return nil
	default:
		return ErrLSPQueueFull
	}
}

func (t *LSPTransport) writeLoop() {
	defer t.wg.Done()
	for {
		select {
		case <-t.done:
			return
		case w := <-t.out:
			err := writeFull(t.stdin, w.frame)
			if w.done != nil {
				w.done <- err
			}
			if err != nil {
				t.fail(err)
				return
			}
		}
	}
}

func (t *LSPTransport) readLoop(fr *frameReader) {
	defer t.wg.Done()
	defer close(t.incoming)
	for {
		body, err := fr.readFrame()
		if err == nil {
			err = t.dispatch(body)
		}
		if err != nil {
			t.fail(err) // io.EOF here means the server closed stdout
			return
		}
	}
}

type lspEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int             `json:"code"`
		Message json.RawMessage `json:"message"`
	} `json:"error"`
}

func (t *LSPTransport) dispatch(body []byte) error {
	var m lspEnvelope
	if err := json.Unmarshal(body, &m); err != nil || m.JSONRPC != "2.0" {
		return ErrLSPBadJSON
	}
	hasID := len(m.ID) > 0 && string(m.ID) != "null"
	switch {
	case m.Method != nil && *m.Method != "":
		msg := LSPMessage{Kind: LSPNotification, Method: *m.Method, Params: m.Params}
		if len(m.ID) > 0 {
			if !validRequestID(m.ID) {
				return ErrLSPBadJSON
			}
			msg.Kind, msg.ID = LSPServerRequest, m.ID
			t.mu.Lock()
			if _, dup := t.srvPending[string(m.ID)]; dup {
				t.mu.Unlock()
				return ErrLSPBadJSON
			}
			t.srvPending[string(m.ID)] = struct{}{}
			t.mu.Unlock()
		}
		select {
		case t.incoming <- msg: // never blocks the reader: a slow consumer overflows visibly
			return nil
		default:
			return ErrLSPQueueFull
		}
	case m.Method == nil && (m.Result != nil || m.Error != nil) && (m.Result == nil || m.Error == nil):
		if !hasID {
			t.mu.Lock()
			t.unmatched++
			t.mu.Unlock()
			return nil
		}
		key := string(m.ID)
		t.mu.Lock()
		ch, ok := t.pending[key]
		if ok {
			delete(t.pending, key)
		} else {
			t.unmatched++
		}
		t.mu.Unlock()
		if !ok {
			return nil
		}
		if m.Error != nil {
			ch <- lspResult{err: &LSPResponseError{Code: m.Error.Code, Message: t.sanitize(m.Error.Message)}}
		} else {
			ch <- lspResult{result: m.Result}
		}
		return nil
	}
	return ErrLSPBadJSON
}

func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	switch id[0] {
	case '"':
		return true
	case 'n':
		return false
	}
	return id[0] == '-' || (id[0] >= '0' && id[0] <= '9')
}

func (t *LSPTransport) sanitize(rawMsg json.RawMessage) string {
	var s string
	if json.Unmarshal(rawMsg, &s) != nil {
		return ""
	}
	return sanitizeText(s, t.limits.MaxErrorMessage)
}

// sanitizeText drops control characters and invalid UTF-8 and cuts to max bytes
// on a rune boundary.
func sanitizeText(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > max {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stderrLoop drains stderr (so the server never blocks on it) keeping only a
// bounded sanitized prefix. It is entirely separate from stdout framing.
func (t *LSPTransport) stderrLoop(r io.Reader) {
	defer t.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			t.mu.Lock()
			room := t.limits.MaxStderrBytes - t.stderr.Len()
			if room > 0 {
				t.stderr.WriteString(sanitizeText(strings.ToValidUTF8(string(buf[:n]), ""), room))
			}
			if room <= 0 || n > room {
				t.stderrCut = true
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return json.RawMessage("null"), nil
	}
	raw, err := json.Marshal(params)
	return raw, err
}

func encodeMessage(msg map[string]any, max int) ([]byte, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return encodeFrame(body, max)
}
