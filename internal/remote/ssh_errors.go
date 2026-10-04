package remote

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

var (
	ErrSSHClientNotFound     = errors.New("native OpenSSH client not found; install the Windows OpenSSH Client optional feature")
	ErrSSHAskpassUnsupported = errors.New("SSH password and passphrase dialogs are not supported on Windows; use key or ssh-agent authentication")
	ErrSSHAuthentication     = errors.New("SSH authentication failed")
	ErrSSHHostKey            = errors.New("SSH host-key verification failed")
)

// sshStderr retains a bounded tail, like Session's stderr capture. Assign it
// to Cmd.Stderr so Wait joins the copy before diagnostics are read.
type sshStderr struct {
	mu   sync.Mutex
	data []byte
}

func (s *sshStderr) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	if len(p) >= stderrCap {
		s.data = append(s.data[:0], p[len(p)-stderrCap:]...)
	} else {
		s.data = append(s.data, p...)
		if len(s.data) > stderrCap {
			s.data = s.data[len(s.data)-stderrCap:]
		}
	}
	return n, nil
}

func (s *sshStderr) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.data))
}

// SSH uses 255 for transport/auth failures; remote commands use their own
// status. Don't mistake a remote filesystem's "Permission denied" for auth.
func sshFailure(err error, stderr string) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 255 {
		switch {
		case strings.Contains(stderr, "Host key verification failed") || strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
			err = fmt.Errorf("%w; verify the host's identity from a terminal before retrying", ErrSSHHostKey)
		case strings.Contains(stderr, "Permission denied (") || strings.Contains(stderr, "Too many authentication failures"):
			err = ErrSSHAuthentication
			if !SupportsSSHAskpass() {
				err = fmt.Errorf("%w; the Windows app requires noninteractive key/agent authentication. Load your key into Windows ssh-agent and verify 'ssh -o BatchMode=yes <host> true' works in a terminal", err)
			}
		}
	}
	if stderr != "" {
		return fmt.Errorf("%w (stderr: %s)", err, stderr)
	}
	return err
}
