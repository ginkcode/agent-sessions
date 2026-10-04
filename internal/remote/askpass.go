package remote

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Maximum size of prompt payload accepted by the askpass broker.
const maxAskpassPayload = 16 << 10 // 16 KiB

// Default timeout for a prompt waiting for user response.
const askpassPromptTimeout = 2 * time.Minute

// AskpassRequest is sent from the askpass helper process to the broker.
type AskpassRequest struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}

// AskpassResponse is returned by the broker to the askpass helper.
type AskpassResponse struct {
	Answer string `json:"answer,omitempty"`
	Error  string `json:"error,omitempty"`
}

// AskpassPromptHandler is invoked when an SSH prompt arrives.
type AskpassPromptHandler func(id string, prompt string)

// AskpassBroker listens on a local unix domain socket to securely prompt the UI
// for credentials during SSH execution.
type AskpassBroker struct {
	sockPath string
	dir      string
	token    string
	listener net.Listener
	handler  AskpassPromptHandler

	mu      sync.Mutex
	pending map[string]chan string
	seq     int64
	closed  bool
	done    chan struct{}
}

// NewAskpassBroker creates a new askpass unix socket broker with a secure token.
func NewAskpassBroker(handler AskpassPromptHandler) (*AskpassBroker, error) {
	if !SupportsSSHAskpass() {
		return nil, ErrSSHAskpassUnsupported
	}
	// Generate random 16-byte token
	tokBytes := make([]byte, 16)
	if _, err := rand.Read(tokBytes); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(tokBytes)

	// Create private 0700 directory for the socket
	baseDir, err := ControlDir()
	if err != nil {
		return nil, err
	}
	// A random name of its own, so the path reveals nothing of the token.
	sockDir, err := os.MkdirTemp(baseDir, "ap-")
	if err != nil {
		return nil, fmt.Errorf("create askpass dir: %w", err)
	}

	sockPath := filepath.Join(sockDir, "askpass.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = os.RemoveAll(sockDir)
		return nil, fmt.Errorf("listen on askpass socket: %w", err)
	}

	// Set socket file permissions to 0600
	_ = os.Chmod(sockPath, 0600)

	b := &AskpassBroker{
		sockPath: sockPath,
		dir:      sockDir,
		token:    token,
		listener: listener,
		handler:  handler,
		pending:  make(map[string]chan string),
		done:     make(chan struct{}),
	}

	go b.acceptLoop()
	return b, nil
}

// SocketPath returns the unix domain socket path.
func (b *AskpassBroker) SocketPath() string {
	return b.sockPath
}

// Token returns the authentication token required by client helper processes.
func (b *AskpassBroker) Token() string {
	return b.token
}

// Reply provides the user's answer for a pending prompt ID.
// Answers are never logged or stored.
func (b *AskpassBroker) Reply(id string, answer string) bool {
	b.mu.Lock()
	ch, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()

	if ok && ch != nil {
		ch <- answer
		close(ch)
		return true
	}
	return false
}

// Cancel cancels a pending prompt.
func (b *AskpassBroker) Cancel(id string) {
	b.mu.Lock()
	ch, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()

	if ok && ch != nil {
		close(ch)
	}
}

// Close closes the broker listener and removes the socket directory.
func (b *AskpassBroker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true

	// Cancel any pending prompts
	for id, ch := range b.pending {
		close(ch)
		delete(b.pending, id)
	}
	b.mu.Unlock()

	_ = b.listener.Close()
	close(b.done)
	_ = os.RemoveAll(b.dir)
	return nil
}

func (b *AskpassBroker) acceptLoop() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			select {
			case <-b.done:
				return
			default:
			}
			return
		}

		go b.handleConn(conn)
	}
}

func (b *AskpassBroker) handleConn(conn net.Conn) {
	defer conn.Close()

	// Verify peer UID on UNIX platforms
	if uconn, ok := conn.(*net.UnixConn); ok {
		if err := checkPeerUID(uconn); err != nil {
			_ = sendAskpassResponse(conn, AskpassResponse{Error: "unauthorized peer"})
			return
		}
	}

	// The helper sends its request at once; a client that stalls must not
	// hold a goroutine forever. Only the read is bounded, not the reply.
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	// Limit reader to avoid denial of service
	r := io.LimitReader(conn, maxAskpassPayload)
	var req AskpassRequest
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		_ = sendAskpassResponse(conn, AskpassResponse{Error: "invalid request payload"})
		return
	}

	// Authenticate token
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(b.token)) != 1 {
		_ = sendAskpassResponse(conn, AskpassResponse{Error: "invalid askpass token"})
		return
	}

	// Register prompt
	promptID := fmt.Sprintf("ap-%d", atomic.AddInt64(&b.seq, 1))
	respCh := make(chan string, 1)

	b.mu.Lock()
	b.pending[promptID] = respCh
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, promptID)
		b.mu.Unlock()
	}()

	// Notify handler
	if b.handler != nil {
		b.handler(promptID, req.Prompt)
	}

	// Wait for user reply or timeout
	select {
	case answer, ok := <-respCh:
		if !ok {
			_ = sendAskpassResponse(conn, AskpassResponse{Error: "prompt cancelled"})
			return
		}
		_ = sendAskpassResponse(conn, AskpassResponse{Answer: answer})

	case <-time.After(askpassPromptTimeout):
		_ = sendAskpassResponse(conn, AskpassResponse{Error: "prompt timed out"})

	case <-b.done:
		_ = sendAskpassResponse(conn, AskpassResponse{Error: "broker closed"})
	}
}

func sendAskpassResponse(conn net.Conn, resp AskpassResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// RunAskpassHelper is invoked when os.Getenv("AGENT_SESSIONS_ASKPASS_SOCK") is present.
// It connects to the broker, transmits the prompt, and writes the answer to stdout.
func RunAskpassHelper(ctx context.Context, sockPath, token, prompt string, stdout, stderr io.Writer) error {
	if !SupportsSSHAskpass() {
		return ErrSSHAskpassUnsupported
	}
	if sockPath == "" {
		sockPath = os.Getenv("AGENT_SESSIONS_ASKPASS_SOCK")
	}
	if token == "" {
		token = os.Getenv("AGENT_SESSIONS_ASKPASS_TOKEN")
	}
	if sockPath == "" || token == "" {
		return errors.New("missing AGENT_SESSIONS_ASKPASS_SOCK or AGENT_SESSIONS_ASKPASS_TOKEN")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err != nil {
		return fmt.Errorf("dial askpass broker: %w", err)
	}
	defer conn.Close()

	req := AskpassRequest{
		Token:  token,
		Prompt: prompt,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	reqData = append(reqData, '\n')
	if _, err := conn.Write(reqData); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read response: %w", err)
	}

	var resp AskpassResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	// Write answer directly to stdout (OpenSSH reads stdout)
	_, _ = fmt.Fprintln(stdout, resp.Answer)
	return nil
}
