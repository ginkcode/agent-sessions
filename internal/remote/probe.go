package remote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// HostProbe reports system details of a remote host.
type HostProbe struct {
	OS   string // "linux" or "darwin"
	Arch string // "amd64" or "arm64"
	Home string // remote user $HOME
	// Installed lists the server builds of this app version on the host
	// ("<version>-<sha8>" directory names), newest first.
	Installed []string
}

// maxInstalled caps the builds a probe reads, against a hostile listing.
const maxInstalled = 64

// Has reports whether the build dir tag is installed.
func (p *HostProbe) Has(tag string) bool {
	return slices.Contains(p.Installed, tag)
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
// and which server builds of appVersion are installed.
func ProbeHost(ctx context.Context, alias string, opts SSHOptions, appVersion string) (*HostProbe, error) {
	nonce := rpc.GenerateNonce()
	preface := rpc.FormatPreface(nonce)

	remoteScript := probeScript(preface, appVersion)

	// Run inside login shell $SHELL -lc to load PATH and env
	shellCmd := []string{LoginShell, "-lc", remoteScript}
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
		if line == "END" {
			break
		}
		if line != "" {
			lines = append(lines, line)
		}
		if len(lines) >= 3+maxInstalled {
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
	for _, line := range lines[3:] {
		tag, ok := strings.CutPrefix(line, "INSTALLED:")
		if ok && isBuildTag(appVersion, tag) {
			probe.Installed = append(probe.Installed, tag)
		}
	}
	return probe, nil
}

// isBuildTag reports whether tag is a build dir of appVersion:
// "<version>-" followed by exactly 8 lowercase hex digits. The prefix alone
// also matches other versions such as "<version>-rc1-…".
func isBuildTag(appVersion, tag string) bool {
	sum, ok := strings.CutPrefix(tag, appVersion+"-")
	if !ok || len(sum) != 8 {
		return false
	}
	for _, c := range sum {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// probeScript prints the preface, uname -s, uname -m, $HOME, one
// "INSTALLED:<dir>" line per server build of appVersion (newest first), and
// END. Every build of the version is listed, not just one, so the client can
// pick the one matching its own bundle: two builds of one version (a
// rebuild, or clients built apart) must not run each other's server.
func probeScript(preface, appVersion string) string {
	return fmt.Sprintf(
		`echo %[1]s; uname -s; uname -m; echo "$HOME"; `+
			`ls -1dt "$HOME"%[2]s%[3]s* 2>/dev/null | while IFS= read -r d; do `+
			`if [ -x "$d/agent-sessions-cli" ]; then echo "INSTALLED:${d##*/}"; fi; done; echo END`,
		QuotePOSIX(preface),
		QuotePOSIX(serverRoot),
		QuotePOSIX(appVersion+"-"),
	)
}
