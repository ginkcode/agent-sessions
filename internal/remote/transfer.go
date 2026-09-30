package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

// validTokenRe mirrors the token validation in agent-sessions-cli transfer.
var validTokenRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// ArtifactTransport transfers artifacts between local and remote hosts.
type ArtifactTransport interface {
	PutArtifact(ctx context.Context, token string, src io.Reader) (string, error)
	GetArtifact(ctx context.Context, token string, remove bool, dst io.Writer) error
	StagingPath(token string) string
}

// RandomArtifactToken generates a unique, safe token for artifact transfer.
func RandomArtifactToken(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate artifact token: %w", err)
	}
	if prefix == "" {
		prefix = "artifact"
	}
	token := fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
	if !validTokenRe.MatchString(token) {
		return "", fmt.Errorf("generated invalid artifact token: %s", token)
	}
	return token, nil
}

// PutArtifact uploads an artifact to remote staging via `agent-sessions-cli transfer put <token>`.
// It returns the sha256 checksum printed by the server on success.
func (s *Session) PutArtifact(ctx context.Context, token string, src io.Reader) (string, error) {
	if s == nil {
		return "", errors.New("no active session")
	}
	if !validTokenRe.MatchString(token) {
		return "", fmt.Errorf("invalid artifact token %q", token)
	}
	opts := s.opts
	opts.NoTTY = true

	cmdBuilder := s.sshCmdFunc
	if cmdBuilder == nil {
		cmdBuilder = BuildSSHCmd
	}

	cmd, err := cmdBuilder(ctx, s.alias, s.transferArgs("put", token), opts)
	if err != nil {
		return "", err
	}
	cmd.Stdin = src
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("artifact put: %w (stderr: %s)", err, msg)
		}
		return "", fmt.Errorf("artifact put: %w", err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// GetArtifact streams an artifact from remote staging via `agent-sessions-cli transfer get <token> [--remove]`.
func (s *Session) GetArtifact(ctx context.Context, token string, remove bool, dst io.Writer) error {
	if s == nil {
		return errors.New("no active session")
	}
	if !validTokenRe.MatchString(token) {
		return fmt.Errorf("invalid artifact token %q", token)
	}
	opts := s.opts
	opts.NoTTY = true

	remoteCmd := s.transferArgs("get", token)
	if remove {
		remoteCmd = append(remoteCmd, "--remove")
	}

	cmdBuilder := s.sshCmdFunc
	if cmdBuilder == nil {
		cmdBuilder = BuildSSHCmd
	}

	cmd, err := cmdBuilder(ctx, s.alias, remoteCmd, opts)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stdout = dst
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("artifact get: %w (stderr: %s)", err, msg)
		}
		return fmt.Errorf("artifact get: %w", err)
	}

	return nil
}

// transferArgs builds the remote transfer command. It passes the cache dir
// serve reported, which already reflects the client's env overrides.
func (s *Session) transferArgs(action, token string) []string {
	args := []string{s.bin, "transfer", action, token}
	if s.init != nil && s.init.Roots.Cache != "" {
		args = append(args, "--cache", s.init.Roots.Cache)
	}
	return args
}

// StagingPath returns the full remote path to the artifact with the given token.
func (s *Session) StagingPath(token string) string {
	if s == nil || s.init == nil || s.init.Roots.Cache == "" {
		return ""
	}
	return path.Join(s.init.Roots.Cache, "staging", token)
}

// SetSSHCmdFunc sets a custom command builder for testing.
func (s *Session) SetSSHCmdFunc(fn func(ctx context.Context, alias string, remoteCmd []string, opts SSHOptions) (*exec.Cmd, error)) {
	s.sshCmdFunc = fn
}
