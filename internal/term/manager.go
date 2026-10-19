// Package term runs the processes of the app's built-in terminal panel in
// pseudo-terminals. Output goes to the frontend as events; input, resizes
// and acknowledgements come back through bindings.
package term

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// Events the manager emits.
const (
	// DataEvent carries output: DataPayload.
	DataEvent = "term:data"
	// ExitEvent reports that a terminal's process exited: ExitPayload.
	ExitEvent = "term:exit"
)

// Tuning of the output path, variables for tests.
var (
	// readChunk is the read buffer size.
	readChunk = 32 << 10
	// flushSize sends batched output at once when this much is pending.
	flushSize = 64 << 10
	// batchDelay is how long output is held to batch it.
	batchDelay = 8 * time.Millisecond
	// maxUnacked pauses reading while more than this much output has not
	// been acknowledged by the frontend.
	maxUnacked = 1 << 20
	// drainGrace is how long the reader may keep reading after the process
	// exits, for output a background process still writes.
	drainGrace = 200 * time.Millisecond
	// killGrace is how long Close waits for the process to exit before it
	// kills it.
	killGrace = 2 * time.Second
	// closeAllWait bounds how long CloseAll waits for every process.
	closeAllWait = 3 * time.Second
)

const (
	defaultCols = 80
	defaultRows = 24
)

// ErrNotFound means no running terminal has the id.
var ErrNotFound = errors.New("terminal not found")

// ErrClosed means the manager was closed.
var ErrClosed = errors.New("terminals are closed")

// Kind is what a terminal tab runs.
type Kind string

const (
	KindAgent Kind = "agent"
	KindShell Kind = "shell"
)

// Spec is what to run in a new terminal.
type Spec struct {
	Argv []string
	// Dir is the working directory; empty means the app's.
	Dir string
	// Env is the environment; nil means the app's. TERM and COLORTERM are
	// always set for xterm.js.
	Env        []string
	Cols, Rows int
}

// Tab describes a running terminal.
type Tab struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Host is where the process runs: "" for this computer, or the WSL or
	// SSH host.
	Host string `json:"host"`
	Kind Kind   `json:"kind"`
	// Dir is the directory it starts in, where known: always on this
	// computer, and for a shell elsewhere. Empty means the home directory
	// or, for an agent elsewhere, unknown here.
	Dir string `json:"dir,omitempty"`
}

// DataPayload is the payload of DataEvent.
type DataPayload struct {
	ID string `json:"id"`
	// Data is the output, base64-encoded.
	Data string `json:"data"`
}

// ExitPayload is the payload of ExitEvent.
type ExitPayload struct {
	ID   string `json:"id"`
	Code int    `json:"code"`
}

// Manager owns the running terminals.
type Manager struct {
	emit engine.Emitter

	mu       sync.Mutex
	sessions map[string]*session
	next     uint64
	closed   bool
}

// NewManager returns a manager that emits output and exits to emit.
func NewManager(emit engine.Emitter) *Manager {
	if emit == nil {
		emit = engine.NopEmitter{}
	}
	return &Manager{emit: emit, sessions: map[string]*session{}}
}

// Start runs spec in a new pseudo-terminal and returns its tab: info with
// the new id.
func (m *Manager) Start(spec Spec, info Tab) (Tab, error) {
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return Tab{}, errors.New("no command to run in the terminal")
	}
	path, err := exec.LookPath(spec.Argv[0])
	if err != nil {
		return Tab{}, fmt.Errorf("could not start %s: %w", spec.Argv[0], err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Tab{}, ErrClosed
	}
	m.next++
	info.ID = fmt.Sprintf("t%d", m.next)
	m.mu.Unlock()

	p, err := pty.New()
	if err != nil {
		return Tab{}, fmt.Errorf("could not open a terminal: %w", err)
	}
	cols, rows := spec.Cols, spec.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = defaultCols, defaultRows
	}
	_ = p.Resize(cols, rows)
	cmd := p.Command(path, spec.Argv[1:]...)
	cmd.Args[0] = spec.Argv[0]
	cmd.Dir = spec.Dir
	cmd.Env = terminalEnv(spec.Env)
	if err := cmd.Start(); err != nil {
		_ = p.Close()
		return Tab{}, fmt.Errorf("could not start %s: %w", spec.Argv[0], err)
	}
	afterStart(p)

	s := &session{
		m:          m,
		tab:        info,
		pty:        p,
		cmd:        cmd,
		readerDone: make(chan struct{}),
		exited:     make(chan struct{}),
		maxUnacked: maxUnacked,
		drainGrace: drainGrace,
		killGrace:  killGrace,
	}
	s.cond = sync.NewCond(&s.mu)

	m.mu.Lock()
	closed := m.closed
	if !closed {
		m.sessions[info.ID] = s
	}
	m.mu.Unlock()
	go s.read()
	go s.wait()
	if closed {
		s.terminate()
		return Tab{}, ErrClosed
	}
	return info, nil
}

// Write sends input to a terminal.
func (m *Manager) Write(id string, data []byte) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = s.pty.Write(data)
	return err
}

// Resize sets a terminal's size in cells.
func (m *Manager) Resize(id string, cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > 0xffff || rows > 0xffff {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return s.pty.Resize(cols, rows)
}

// Ack acknowledges n bytes of output the frontend has written.
func (m *Manager) Ack(id string, n int) {
	if s, err := m.get(id); err == nil {
		s.ack(n)
	}
}

// Close ends a terminal's process: its process group gets SIGHUP on Unix
// and is killed if it is still running after a grace period. A terminal
// that already exited is not an error.
func (m *Manager) Close(id string) error {
	s, err := m.get(id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	s.terminate()
	return nil
}

// CloseAll ends every terminal and refuses new ones. It waits a bounded
// time for the processes to exit.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.terminate()
	}
	deadline := time.After(closeAllWait)
	for _, s := range sessions {
		select {
		case <-s.exited:
		case <-deadline:
			return
		}
	}
}

// Running reports whether a terminal runs on host.
func (m *Manager) Running(host string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.tab.Host == host {
			return true
		}
	}
	return false
}

// Tab describes a running tab.
func (m *Manager) Tab(id string) (Tab, error) {
	s, err := m.get(id)
	if err != nil {
		return Tab{}, err
	}
	return s.tab, nil
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return s, nil
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// terminalEnv is env, or the app's, with xterm.js's capabilities.
func terminalEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+2)
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(key) {
		case "TERM", "COLORTERM":
			continue
		}
		out = append(out, e)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// session is one running terminal.
type session struct {
	m   *Manager
	tab Tab
	pty pty.Pty
	cmd *pty.Cmd

	// mu guards the output state; cond wakes a reader paused by flow
	// control.
	mu      sync.Mutex
	cond    *sync.Cond
	pending []byte
	timer   *time.Timer
	unacked int
	// closing lets the reader drain without waiting for acknowledgements.
	closing bool

	// emitMu keeps flushes in order.
	emitMu sync.Mutex

	closeOnce  sync.Once
	killOnce   sync.Once
	readerDone chan struct{}
	exited     chan struct{}

	// The tuning variables, read once.
	maxUnacked            int
	drainGrace, killGrace time.Duration
}

func (s *session) read() {
	defer close(s.readerDone)
	buf := make([]byte, readChunk)
	for {
		s.mu.Lock()
		for s.unacked > s.maxUnacked && !s.closing {
			s.cond.Wait()
		}
		s.mu.Unlock()
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.push(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// push batches output: it is sent when flushSize is pending or batchDelay
// after the first byte.
func (s *session) push(b []byte) {
	s.mu.Lock()
	s.pending = append(s.pending, b...)
	if len(s.pending) >= flushSize {
		s.mu.Unlock()
		s.flush()
		return
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(batchDelay, s.flush)
	}
	s.mu.Unlock()
}

func (s *session) flush() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	data := s.pending
	s.pending = nil
	s.unacked += len(data)
	s.mu.Unlock()
	if len(data) == 0 {
		return
	}
	s.m.emit.Emit(DataEvent, DataPayload{ID: s.tab.ID, Data: base64.StdEncoding.EncodeToString(data)})
}

func (s *session) ack(n int) {
	s.mu.Lock()
	s.unacked -= n
	if s.unacked < 0 {
		s.unacked = 0
	}
	s.cond.Broadcast()
	s.mu.Unlock()
}

// unpause lets the reader run to the end regardless of acknowledgements.
func (s *session) unpause() {
	s.mu.Lock()
	s.closing = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *session) closePty() {
	s.closeOnce.Do(func() { _ = s.pty.Close() })
}

// wait reaps the process, lets the reader drain, and reports the exit.
func (s *session) wait() {
	err := s.cmd.Wait()
	code := exitCode(s.cmd.ProcessState, err)
	select {
	case <-s.readerDone:
	case <-time.After(s.drainGrace):
	}
	s.unpause()
	s.closePty()
	<-s.readerDone
	s.flush()
	s.m.remove(s.tab.ID)
	// Report the exit before signalling it, so CloseAll returns only once
	// every terminal's exit has been reported.
	s.m.emit.Emit(ExitEvent, ExitPayload{ID: s.tab.ID, Code: code})
	close(s.exited)
}

func exitCode(state *os.ProcessState, err error) int {
	if state == nil {
		if err != nil {
			return -1
		}
		return 0
	}
	return stateCode(state)
}
