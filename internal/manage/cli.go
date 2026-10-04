package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/redact"
)

// ErrCLINotFound reports that a provider CLI is neither on PATH nor in any
// of its usual install locations.
var ErrCLINotFound = errors.New("CLI not found")

// resolveCLI returns an absolute path for name: PATH first, then the
// locations launch.Find searches. A desktop launcher does not read shell rc
// files, so PATH additions made there are missing when the app starts from
// a menu. On Windows the locations include the copies bundled with the
// desktop apps (OpenCode Desktop's opencode-cli.exe, Codex desktop's
// codex.exe) that desktop-only users have instead of a CLI on PATH.
func resolveCLI(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	exe, err := launch.Find(name)
	if err != nil {
		return "", fmt.Errorf("%s %w on PATH or in its usual install locations", name, ErrCLINotFound)
	}
	return exe.Path, nil
}

// withPathPrefix puts dir first on PATH so a script CLI (an npm shim that
// runs `env node`) finds the interpreter installed next to it.
func withPathPrefix(env []string, dir string) []string {
	path := os.Getenv("PATH")
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	if path == "" {
		path = dir
	} else {
		path = dir + string(os.PathListSeparator) + path
	}
	return append(env, "PATH="+path)
}

// cliMessage reduces captured stderr to one displayable line: the last
// non-empty line, with secrets redacted, the home directory shortened to ~,
// control characters dropped and the length capped.
func cliMessage(stderr []byte) string {
	var line string
	for _, l := range bytes.Split(stderr, []byte("\n")) {
		if s := strings.TrimSpace(string(l)); s != "" {
			line = s
		}
	}
	if line == "" {
		return ""
	}
	line, _ = redact.Text(line)
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 {
		line = strings.ReplaceAll(line, home, "~")
	}
	line = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, line)
	const maxLen = 240
	if r := []rune(line); len(r) > maxLen {
		line = string(r[:maxLen]) + "…"
	}
	return line
}

// cliFailure describes a failed CLI run without echoing its full output.
func cliFailure(name string, err error, stderr []byte) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s was interrupted: %w", name, err)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		msg := fmt.Sprintf("%s exited with status %d", name, exitErr.ExitCode())
		if detail := cliMessage(stderr); detail != "" {
			msg += ": " + detail
		}
		return errors.New(msg)
	}
	return fmt.Errorf("%s could not be started", name)
}
