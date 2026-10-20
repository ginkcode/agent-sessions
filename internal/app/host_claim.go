package app

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/filelock"
)

// Each app window is its own process. A host is open in at most one of
// them: the window connected to it, or connecting, holds the host's claim,
// a file lock under the cache dir. Choosing that host in another window
// brings the holder's window to the front instead of opening it twice.
//
// Next to its lock the holder writes where to reach it: a loopback port and
// a random token. The token keeps other local users, who can reach the
// port but cannot read the 0600 file, from raising the window.

// ErrHostInOtherWindow means another window of the app has the host open.
var ErrHostInOtherWindow = errors.New("is open in another window")

// hostInUseError names the host another window has open, and whether that
// window was brought to the front.
type hostInUseError struct {
	host     string
	focusErr error
}

func (e *hostInUseError) Error() string {
	msg := fmt.Sprintf("%s %v", e.host, ErrHostInOtherWindow)
	if e.focusErr != nil {
		msg += fmt.Sprintf(": could not bring it to the front: %v", e.focusErr)
	}
	return msg
}

func (e *hostInUseError) Unwrap() error { return ErrHostInOtherWindow }

// focusTimeout bounds a request to another window, which may be stalled.
const focusTimeout = 2 * time.Second

// claimInfo is the file next to a held claim's lock.
type claimInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

type hostClaims struct {
	dir string
	// focus brings this process's window to the front.
	focus func()

	mu    sync.Mutex
	held  map[string]*filelock.Lock
	ln    net.Listener
	token string
	// ringLock and ringID are this window's place in the window ring
	// (window_ring.go); nil until it joins.
	ringLock *filelock.Lock
	ringID   string
}

func newHostClaims(dir string, focus func()) *hostClaims {
	return &hostClaims{dir: dir, focus: focus, held: map[string]*filelock.Lock{}}
}

// claimFile is the path of host's claim files without an extension. Hashing
// keeps any alias a safe, fixed-length file name.
func (h *hostClaims) claimFile(host string) string {
	sum := sha256.Sum256([]byte(host))
	return filepath.Join(h.dir, "host-"+hex.EncodeToString(sum[:12]))
}

// claim makes this window the one that has host open. It fails with a
// *hostInUseError when another window has it.
func (h *hostClaims) claim(host string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held[host] != nil {
		return nil
	}
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return fmt.Errorf("create host claim dir: %w", err)
	}
	base := h.claimFile(host)
	lock, err := filelock.TryLock(base + ".lock")
	if errors.Is(err, filelock.ErrLocked) {
		return &hostInUseError{host: host}
	}
	if err != nil {
		return err
	}
	if err := h.listenLocked(); err != nil {
		_ = lock.Unlock()
		return err
	}
	data, err := json.Marshal(claimInfo{Port: h.ln.Addr().(*net.TCPAddr).Port, Token: h.token})
	if err == nil {
		err = configfile.WriteAtomic(base+".json", data)
	}
	if err != nil {
		_ = lock.Unlock()
		return fmt.Errorf("write host claim: %w", err)
	}
	h.held[host] = lock
	return nil
}

// release gives up host, if held, so another window can open it.
func (h *hostClaims) release(host string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.releaseLocked(host)
}

func (h *hostClaims) releaseLocked(host string) {
	lock := h.held[host]
	if lock == nil {
		return
	}
	delete(h.held, host)
	// The info goes first: once unlocked, the next holder writes its own.
	_ = os.Remove(h.claimFile(host) + ".json")
	_ = lock.Unlock()
}

// close releases every claim and stops answering focus requests.
func (h *hostClaims) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for host := range h.held {
		h.releaseLocked(host)
	}
	h.leaveRingLocked()
	if h.ln != nil {
		_ = h.ln.Close()
		h.ln = nil
	}
}

// focusOwner asks the window that has host open to come to the front.
func (h *hostClaims) focusOwner(host string) error {
	data, err := os.ReadFile(h.claimFile(host) + ".json")
	if err != nil {
		return err
	}
	var info claimInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	if info.Port <= 0 || info.Token == "" {
		return errors.New("incomplete host claim")
	}
	return h.focusVia(info)
}

// focusVia asks the window listening at info to come to the front.
func (h *hostClaims) focusVia(info claimInfo) error {
	// This window is in front, so it may hand the foreground on (Windows).
	allowForegroundHandoff()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(info.Port)), focusTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(focusTimeout))
	if _, err := conn.Write([]byte(info.Token + "\n")); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "ok" {
		return errors.New("refused")
	}
	return nil
}

func (h *hostClaims) listenLocked() error {
	if h.ln != nil {
		return nil
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for focus requests: %w", err)
	}
	h.ln, h.token = ln, hex.EncodeToString(tok)
	go h.serve(ln, h.token)
	return nil
}

func (h *hostClaims) serve(ln net.Listener, token string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go h.answer(conn, token)
	}
}

func (h *hostClaims) answer(conn net.Conn, token string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(focusTimeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(line)), []byte(token)) != 1 {
		return
	}
	if h.focus != nil {
		h.focus()
	}
	_, _ = conn.Write([]byte("ok\n"))
}
