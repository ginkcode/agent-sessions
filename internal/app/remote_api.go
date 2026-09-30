package app

import (
	"github.com/ginkcode/agent-sessions/internal/remote"
)

// ListHosts returns SSH config aliases. Manual entry is not represented here;
// Connect accepts any alias that passes validation.
func (a *App) ListHosts() ([]HostEntry, error) {
	aliases, err := remote.ListConfigHosts()
	if err != nil {
		return nil, err
	}
	out := make([]HostEntry, 0, len(aliases))
	for _, h := range aliases {
		out = append(out, HostEntry{
			Name:     h.Name,
			HostName: h.HostName,
			User:     h.User,
			Port:     h.Port,
		})
	}
	return out, nil
}

// Connect starts a connection to alias. The call returns once the attempt is
// underway; progress arrives on the connection:state event. Local remains the
// active backend until the handshake completes.
func (a *App) Connect(alias string) error {
	a.ensureConn()
	return a.conn.Connect(a.appCtx(), alias)
}

// Disconnect closes the remote session and returns the app to Local.
func (a *App) Disconnect() {
	a.ensureConn()
	a.conn.Disconnect()
}

// ConnectionState returns the current connection snapshot.
func (a *App) ConnectionState() ConnectionState {
	a.ensureConn()
	return a.conn.snapshot()
}

// AskpassReply sends an answer for an active askpass prompt.
func (a *App) AskpassReply(id, answer string) bool {
	a.ensureConn()
	return a.conn.askpassReply(id, answer)
}

// SetHostEnv configures environment variable overrides passed to remote servers on connect.
func (a *App) SetHostEnv(env map[string]string) {
	a.ensureConn()
	a.conn.setEnv(env)
}

// GetHostEnv returns currently configured environment variable overrides.
func (a *App) GetHostEnv() map[string]string {
	a.ensureConn()
	return a.conn.getEnv()
}

func (a *App) ensureConn() {
	a.backendMu.Lock()
	defer a.backendMu.Unlock()
	if a.conn == nil {
		a.conn = newConnection(a.wailsEvents)
	}
}
