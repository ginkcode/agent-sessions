//go:build windows

package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestWSLSmoke runs the transport against a real distribution: listing,
// probe, a binary round trip through stdin and stdout, and exit statuses.
// Opt in with AGENT_SESSIONS_WSL_SMOKE=<distro>.
func TestWSLSmoke(t *testing.T) {
	distro := os.Getenv("AGENT_SESSIONS_WSL_SMOKE")
	if distro == "" {
		t.Skip("set AGENT_SESSIONS_WSL_SMOKE=<distro> to run against WSL")
	}
	host := WSLTarget(distro)
	ctx := context.Background()

	distros, err := ListWSLDistros()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("distros: %+v", distros)
	if err := CheckWSLDistro(distro); err != nil {
		t.Fatal(err)
	}
	if err := CheckWSLDistro("NoSuchDistro"); !errors.Is(err, ErrWSLDistroNotFound) {
		t.Errorf("missing distro: %v", err)
	}

	probe, err := ProbeHost(ctx, host, SSHOptions{}, "v0.0.0-smoke")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe: %+v", probe)
	if probe.OS != "linux" || probe.Home == "" {
		t.Errorf("probe = %+v", probe)
	}

	// --exec skips a shell, but WSL should still export the user's shell.
	// The server uses it both for its login environment and terminal scripts.
	var shell bytes.Buffer
	if err := runRemote(ctx, host, SSHOptions{}, &shell, nil, `printf '%s' "${SHELL:-}"`); err != nil {
		t.Fatal(err)
	}
	t.Logf("SHELL under --exec: %s", shell.String())
	if !strings.HasPrefix(shell.String(), "/") {
		t.Errorf("WSL did not export an absolute SHELL: %q", shell.String())
	}

	data := make([]byte, 5<<20)
	_, _ = rand.Read(data)
	var out bytes.Buffer
	if err := runRemote(ctx, host, SSHOptions{}, &out, bytes.NewReader(data), "cat"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("cat returned %d bytes, not the %d sent", out.Len(), len(data))
	}
	out.Reset()
	if err := runRemote(ctx, host, SSHOptions{}, &out, bytes.NewReader(data), "sha256sum"); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(data)); !strings.HasPrefix(out.String(), want) {
		t.Errorf("sha256sum = %q, want %s", out.String(), want)
	}

	cmd, err := BuildHostCmd(ctx, host, []string{LoginShell, "-lc", fmt.Sprintf("exit %d", exitServerMissing)}, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != exitServerMissing {
		t.Errorf("exit status: %v", err)
	}
}
