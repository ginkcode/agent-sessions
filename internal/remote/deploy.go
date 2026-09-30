package remote

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// ServerDirTag is the remote directory name for one deployed server build:
// "<version>-<sha8>".
func ServerDirTag(appVersion, sha256hex string) string {
	sum := strings.TrimSpace(sha256hex)
	if len(sum) > 8 {
		sum = sum[:8]
	}
	return appVersion + "-" + sum
}

// RemoteServerPath is the installed binary for one version tag.
func RemoteServerPath(home, versionTag string) string {
	return strings.TrimRight(home, "/") + "/.cache/agent-sessions/server/" + versionTag + "/agent-sessions-cli"
}

// DeployServer streams a bundled server archive to the remote host, unpacks it
// to a temporary file, verifies sha256 when the remote has sha256sum, sets
// mode 0700, renames it into place, checks `version` output, and prunes older
// builds. localGzPath may be empty to locate the bundle for probe.OS/Arch.
// It returns the absolute remote path of the installed binary.
func DeployServer(ctx context.Context, alias string, opts SSHOptions, probe *HostProbe, localGzPath string) (string, error) {
	if probe == nil {
		return "", fmt.Errorf("deploy: nil probe")
	}
	if probe.Home == "" || strings.ContainsAny(probe.Home, "\n\r") {
		return "", fmt.Errorf("deploy: invalid remote home %q", probe.Home)
	}
	if localGzPath == "" {
		var err error
		localGzPath, err = LocateServer(probe.OS, probe.Arch)
		if err != nil {
			return "", fmt.Errorf("locate server bundle: %w", err)
		}
	}

	sum, err := fileSHA256(localGzPath)
	if err != nil {
		return "", fmt.Errorf("hash server bundle: %w", err)
	}
	tag := ServerDirTag(version.Current(), sum)
	targetBin := RemoteServerPath(probe.Home, tag)
	tmpBin := targetBin + ".tmp"
	targetDir := strings.TrimSuffix(targetBin, "/agent-sessions-cli")

	gz, err := os.Open(localGzPath)
	if err != nil {
		return "", fmt.Errorf("open server bundle: %w", err)
	}
	defer gz.Close()

	// Stream the archive into a temp file, checksum it, then rename.
	// sha256sum is best-effort: macOS and minimal images may not have it.
	unpack := fmt.Sprintf(
		`mkdir -p %[1]s && chmod 700 %[1]s && rm -f %[2]s && gzip -dc > %[2]s && chmod 700 %[2]s && if command -v sha256sum >/dev/null 2>&1; then echo "%[3]s  %[2]s" | sha256sum -c - || exit 86; fi && mv -f %[2]s %[4]s`,
		QuotePOSIX(targetDir),
		QuotePOSIX(tmpBin),
		sum,
		QuotePOSIX(targetBin),
	)
	if err := runRemote(ctx, alias, opts, nil, gz, unpack); err != nil {
		return "", fmt.Errorf("deploy server: %w", err)
	}

	if err := verifyRemoteVersion(ctx, alias, opts, targetBin); err != nil {
		return "", err
	}

	// Best-effort: keep the two newest version directories.
	prune := fmt.Sprintf(
		`cd %[1]s 2>/dev/null && ls -1dt -- */ 2>/dev/null | tail -n +3 | while IFS= read -r d; do rm -rf -- "$d"; done || true`,
		QuotePOSIX(strings.TrimSuffix(targetDir, "/"+tag)),
	)
	_ = runRemote(ctx, alias, opts, nil, nil, prune)

	return targetBin, nil
}

// verifyRemoteVersion runs the deployed binary's version command behind the
// preface and requires it to match the local application version.
func verifyRemoteVersion(ctx context.Context, alias string, opts SSHOptions, bin string) error {
	nonce := rpc.GenerateNonce()
	script := fmt.Sprintf(
		`echo %s; %s version`,
		QuotePOSIX(rpc.FormatPreface(nonce)),
		QuotePOSIX(bin),
	)
	var stdout bytes.Buffer
	if err := runRemote(ctx, alias, opts, &stdout, nil, script); err != nil {
		return fmt.Errorf("verify deployed server: %w", err)
	}
	r, err := rpc.WaitForPreface(ctx, &stdout, nonce)
	if err != nil {
		return fmt.Errorf("verify deployed server handshake: %w", err)
	}
	got, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("verify deployed server: read version: %w", err)
	}
	got = strings.TrimSpace(got)
	want := version.Current()
	if got != want {
		return fmt.Errorf("deployed server version %q, want %q", got, want)
	}
	return nil
}

// runRemote executes script inside a login shell on alias. stdin, when set,
// is the ssh process stdin (used to stream the gzip archive).
func runRemote(ctx context.Context, alias string, opts SSHOptions, stdout io.Writer, stdin io.Reader, script string) error {
	opts.NoTTY = true
	cmd, err := BuildSSHCmd(ctx, alias, []string{"$SHELL", "-lc", script}, opts)
	if err != nil {
		return err
	}
	cmd.Stdin = stdin
	if stdout != nil {
		cmd.Stdout = stdout
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w (stderr: %s)", err, msg)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
