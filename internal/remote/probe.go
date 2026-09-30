package remote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// HostProbe reports system details of a remote host.
type HostProbe struct {
	OS               string // "linux" or "darwin"
	Arch             string // "amd64" or "arm64"
	Home             string // remote user $HOME
	InstalledVersion string // version if installed, or empty
	ServerPath       string // path to server executable if installed
}

// Sentinel errors for probe failures.
var (
	ErrUnsupportedOS   = errors.New("unsupported remote operating system")
	ErrUnsupportedArch = errors.New("unsupported remote architecture")
)

// NormalizeOS converts uname -s output to standard Go GOOS ("linux" or "darwin").
func NormalizeOS(s string) (string, error) {
	val := strings.ToLower(strings.TrimSpace(s))
	switch val {
	case "linux":
		return "linux", nil
	case "darwin":
		return "darwin", nil
	default:
		return "", fmt.Errorf("%w: %s (only Linux and macOS supported)", ErrUnsupportedOS, s)
	}
}

// NormalizeArch converts uname -m output to standard Go GOARCH ("amd64" or "arm64").
func NormalizeArch(m string) (string, error) {
	val := strings.ToLower(strings.TrimSpace(m))
	switch val {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("%w: %s (only x86_64/amd64 and aarch64/arm64 supported)", ErrUnsupportedArch, m)
	}
}

// ProbeHost probes a remote machine via SSH to detect OS, architecture, $HOME,
// and whether the target server version is already installed.
func ProbeHost(ctx context.Context, alias string, opts SSHOptions, expectedVersionTag string) (*HostProbe, error) {
	nonce := rpc.GenerateNonce()
	preface := rpc.FormatPreface(nonce)

	// expectedVersionTag may be a full directory name ("0.3.0-0123abcd") or a
	// version prefix ("0.3.0"). A prefix matches the newest directory that
	// starts with it, which is how a redeploy of the same version is found
	// without knowing the bundle checksum yet.
	remoteScript := fmt.Sprintf(
		`echo %[1]s; uname -s; uname -m; echo "$HOME"; d=$(ls -1dt "$HOME"/.cache/agent-sessions/server/%[2]s* 2>/dev/null | head -n 1); if [ -n "$d" ] && [ -x "$d/agent-sessions-cli" ]; then echo "INSTALLED:$d/agent-sessions-cli"; else echo "NOT_INSTALLED"; fi`,
		QuotePOSIX(preface),
		QuotePOSIX(expectedVersionTag),
	)

	// Run inside login shell $SHELL -lc to load PATH and env
	shellCmd := []string{"$SHELL", "-lc", remoteScript}
	cmd, err := BuildSSHCmd(ctx, alias, shellCmd, opts)
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ssh probe: %w", err)
	}

	var stderrBuf strings.Builder
	go func() {
		_, _ = io.Copy(&stderrBuf, stderr)
	}()

	// Wait for nonce preface (skipping login shell banners up to 64 KiB)
	r, err := rpc.WaitForPreface(ctx, stdout, nonce)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("probe handshake: %w (stderr: %s)", err, stderrBuf.String())
	}

	scanner := bufio.NewScanner(r)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
		if len(lines) >= 4 {
			break
		}
	}

	_ = cmd.Wait()

	if len(lines) < 3 {
		return nil, fmt.Errorf("probe output truncated (got %d lines, want at least 3, stderr: %s)", len(lines), stderrBuf.String())
	}

	goOS, err := NormalizeOS(lines[0])
	if err != nil {
		return nil, err
	}
	goArch, err := NormalizeArch(lines[1])
	if err != nil {
		return nil, err
	}
	home := lines[2]

	probe := &HostProbe{
		OS:   goOS,
		Arch: goArch,
		Home: home,
	}

	if len(lines) >= 4 && strings.HasPrefix(lines[3], "INSTALLED:") {
		probe.InstalledVersion = expectedVersionTag
		probe.ServerPath = strings.TrimPrefix(lines[3], "INSTALLED:")
	}

	return probe, nil
}
