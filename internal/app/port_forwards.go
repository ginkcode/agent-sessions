package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/configfile"
	"github.com/ginkcode/agent-sessions/internal/remote"
)

// PortForwardRequest is a saved rule. Empty ID creates a new rule; otherwise
// SavePortForward updates the rule on the currently selected SSH host.
type PortForwardRequest struct {
	ID         string `json:"id"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	AutoStart  bool   `json:"autoStart"`
}
type PortForwardRule struct {
	PortForwardRequest
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
type PortForwardSnapshot struct {
	Host  string            `json:"host"`
	Rules []PortForwardRule `json:"rules"`
}
type forwardRun struct{ cancel context.CancelFunc }

// portForwards holds the saved rules and this process's running forwards.
// Several app windows (processes) share the rules file: each reads it
// again before use, and changes it under the config lock from its current
// contents, so one window's edits never overwrite another's.
type portForwards struct {
	mu   sync.Mutex
	path string
	// pathErr means the rules file has no usable location; it is never
	// read or written.
	pathErr    error
	loadErr    error
	saved      map[string][]PortForwardRequest
	states     map[string]PortForwardRule
	runs       map[string]*forwardRun
	host       string
	connected  bool
	generation uint64
	opts       remote.SSHOptions
}

func newPortForwards(path string) *portForwards {
	p := &portForwards{path: path, saved: map[string][]PortForwardRequest{}, states: map[string]PortForwardRule{}, runs: map[string]*forwardRun{}}
	p.loadLocked()
	return p
}

// loadLocked reads the rules file again, picking up changes made by other
// windows. A file that cannot be read keeps the last rules and sets loadErr,
// which blocks writes so they never overwrite rules this window did not see.
func (p *portForwards) loadLocked() {
	if p.pathErr != nil {
		p.loadErr = p.pathErr
		return
	}
	data, err := os.ReadFile(p.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		p.saved, p.loadErr = map[string][]PortForwardRequest{}, nil
		return
	case err != nil:
		p.loadErr = err
		return
	}
	saved := map[string][]PortForwardRequest{}
	if err := json.Unmarshal(data, &saved); err != nil {
		p.loadErr = err
		return
	}
	if saved == nil {
		saved = map[string][]PortForwardRequest{}
	}
	p.saved, p.loadErr = saved, nil
}

// updateLocked changes the rules file under the config lock: it reads the
// file as it is now, lets edit change a copy of every host's rules and
// writes the result.
func (p *portForwards) updateLocked(edit func(saved map[string][]PortForwardRequest) error) error {
	if p.pathErr != nil {
		return fmt.Errorf("read port forwards: %w", p.pathErr)
	}
	lock, err := configfile.Lock(p.path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	p.loadLocked()
	if p.loadErr != nil {
		return fmt.Errorf("read port forwards: %w", p.loadErr)
	}
	saved := make(map[string][]PortForwardRequest, len(p.saved))
	for host, rs := range p.saved {
		saved[host] = append([]PortForwardRequest{}, rs...)
	}
	if err := edit(saved); err != nil {
		return err
	}
	return p.persistLocked(saved)
}

// sync is called with connection.mu held, serializing lifecycle changes with
// API operations. It never waits for an SSH process to authenticate or exit.
func (p *portForwards) sync(st ConnectionState, opts remote.SSHOptions) {
	p.mu.Lock()
	defer p.mu.Unlock()
	host := st.Host
	if _, wsl := remote.ParseWSLTarget(host); wsl {
		host = ""
	}
	connected := st.Phase == ConnConnected && host != ""
	changed := p.host != host || p.generation != st.Generation || p.connected != connected
	p.opts = opts
	if !changed {
		return
	}
	for id, run := range p.runs {
		run.cancel()
		delete(p.runs, id)
		rule := p.states[id]
		rule.Status = "disconnected"
		rule.Error = ""
		p.states[id] = rule
	}
	p.host, p.connected, p.generation, p.opts = host, connected, st.Generation, opts
	if connected {
		// Another window may have changed the rules since they were read.
		p.loadLocked()
		for _, rule := range p.saved[host] {
			if rule.AutoStart {
				p.startLocked(rule)
			}
		}
	}
}
func (p *portForwards) snapshotLocked() PortForwardSnapshot {
	out := PortForwardSnapshot{Host: p.host, Rules: []PortForwardRule{}}
	for _, rule := range p.saved[p.host] {
		state, ok := p.states[rule.ID]
		if !ok {
			state = PortForwardRule{PortForwardRequest: rule, Status: "stopped"}
		}
		if !p.connected {
			state.Status = "disconnected"
		}
		out.Rules = append(out.Rules, state)
	}
	return out
}
func (p *portForwards) persistLocked(saved map[string][]PortForwardRequest) error {
	if p.loadErr != nil {
		return fmt.Errorf("read port forwards: %w", p.loadErr)
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p.path), ".port-forwards-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, p.path); err != nil {
		return err
	}
	p.saved = saved
	return nil
}
func (p *portForwards) startLocked(rule PortForwardRequest) {
	if p.runs[rule.ID] != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &forwardRun{cancel: cancel}
	p.runs[rule.ID] = run
	p.states[rule.ID] = PortForwardRule{PortForwardRequest: rule, Status: "starting"}
	host, opts := p.host, p.opts
	go func() {
		log := &forwardLog{ready: func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.runs[rule.ID] == run {
				state := p.states[rule.ID]
				state.Status = "active"
				p.states[rule.ID] = state
			}
		}}
		cmd, err := remote.BuildForwardCmd(ctx, host, rule.RemoteHost, rule.RemotePort, rule.LocalPort, opts)
		if err == nil {
			cmd.Stderr = log
			err = cmd.Run()
		}
		cancel()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.runs[rule.ID] != run {
			return
		}
		delete(p.runs, rule.ID)
		message := log.failure()
		if message == "" && err != nil {
			message = err.Error()
		}
		if message == "" {
			message = "SSH forwarding process exited"
		}
		p.states[rule.ID] = PortForwardRule{PortForwardRequest: rule, Status: "failed", Error: message}
	}()
}

// forwardLog bounds diagnostics and recognizes OpenSSH's post-authentication
// readiness marker, rather than assuming a successfully spawned process works.
type forwardLog struct {
	pending     string
	diagnostics string
	listening   bool
	ready       func()
}

func (l *forwardLog) Write(b []byte) (int, error) {
	l.pending += string(b)
	for {
		line, rest, ok := strings.Cut(l.pending, "\n")
		if !ok {
			break
		}
		l.pending = rest
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Local forwarding listening on 127.0.0.1 port ") {
			l.listening = true
		}
		if l.listening && strings.Contains(line, "Entering interactive session") {
			l.ready()
		}
		if !strings.HasPrefix(line, "debug") && line != "" {
			l.diagnostics += line + "\n"
			if len(l.diagnostics) > 8192 {
				l.diagnostics = l.diagnostics[len(l.diagnostics)-8192:]
			}
		}
	}
	if len(l.pending) > 8192 {
		l.pending = l.pending[len(l.pending)-8192:]
	}
	return len(b), nil
}
func (l *forwardLog) failure() string { return strings.TrimSpace(l.diagnostics + l.pending) }

func (a *App) withForwards(fn func(*portForwards) error) (PortForwardSnapshot, error) {
	a.ensureConn()
	c := a.conn
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.forwards
	if c.stopped {
		p.sync(ConnectionState{Phase: ConnLocal}, c.opts)
	} else {
		p.sync(c.state, c.opts)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
	if p.loadErr != nil {
		return p.snapshotLocked(), fmt.Errorf("read port forwards: %w", p.loadErr)
	}
	err := fn(p)
	return p.snapshotLocked(), err
}
func (a *App) ListPortForwards() (PortForwardSnapshot, error) {
	return a.withForwards(func(p *portForwards) error { return nil })
}
func (a *App) SavePortForward(rule PortForwardRequest) (PortForwardSnapshot, error) {
	return a.withForwards(func(p *portForwards) error {
		if p.host == "" {
			return fmt.Errorf("select an SSH host first")
		}
		if err := remote.ValidateForwardTarget(rule.RemoteHost, rule.RemotePort, rule.LocalPort); err != nil {
			return err
		}
		err := p.updateLocked(func(saved map[string][]PortForwardRequest) error {
			rules := saved[p.host]
			idx := -1
			for i, r := range rules {
				if r.ID == rule.ID {
					idx = i
				}
				if r.ID != rule.ID && r.LocalPort == rule.LocalPort {
					return fmt.Errorf("local port already used by another rule")
				}
			}
			if rule.ID == "" {
				var id [16]byte
				if _, err := rand.Read(id[:]); err != nil {
					return err
				}
				rule.ID = hex.EncodeToString(id[:])
				rules = append(rules, rule)
			} else {
				if idx < 0 {
					return fmt.Errorf("port forward not found")
				}
				rules[idx] = rule
			}
			saved[p.host] = rules
			return nil
		})
		if err != nil {
			return err
		}
		if run := p.runs[rule.ID]; run != nil {
			run.cancel()
			delete(p.runs, rule.ID)
		}
		p.states[rule.ID] = PortForwardRule{PortForwardRequest: rule, Status: "stopped"}
		return nil
	})
}
func (a *App) StartPortForward(id string) (PortForwardSnapshot, error) {
	return a.withForwards(func(p *portForwards) error {
		if a.conn.stopped || !p.connected {
			return fmt.Errorf("SSH host is disconnected")
		}
		for _, rule := range p.saved[p.host] {
			if rule.ID == id {
				p.startLocked(rule)
				return nil
			}
		}
		return fmt.Errorf("port forward not found")
	})
}
func (a *App) StopPortForward(id string) (PortForwardSnapshot, error) {
	return a.withForwards(func(p *portForwards) error {
		for _, rule := range p.saved[p.host] {
			if rule.ID == id {
				if run := p.runs[id]; run != nil {
					run.cancel()
					delete(p.runs, id)
				}
				p.states[id] = PortForwardRule{PortForwardRequest: rule, Status: "stopped"}
				return nil
			}
		}
		return fmt.Errorf("port forward not found")
	})
}
func (a *App) RemovePortForward(id string) (PortForwardSnapshot, error) {
	return a.withForwards(func(p *portForwards) error {
		err := p.updateLocked(func(saved map[string][]PortForwardRequest) error {
			rules := []PortForwardRequest{}
			found := false
			for _, rule := range saved[p.host] {
				if rule.ID == id {
					found = true
				} else {
					rules = append(rules, rule)
				}
			}
			if !found {
				return fmt.Errorf("port forward not found")
			}
			saved[p.host] = rules
			return nil
		})
		if err != nil {
			return err
		}
		if run := p.runs[id]; run != nil {
			run.cancel()
			delete(p.runs, id)
		}
		delete(p.states, id)
		return nil
	})
}
