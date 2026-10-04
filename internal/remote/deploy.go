package remote

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// ErrServerVersionMismatch means the bundled server reports another version
// than the app. Redeploying the same bundle cannot fix it.
var ErrServerVersionMismatch = errors.New("server version mismatch")

// ServerDirTag is the remote directory name for one deployed server build:
// "<version>-<sha8>".
func ServerDirTag(appVersion, sha256hex string) string {
	sum := strings.TrimSpace(sha256hex)
	if len(sum) > 8 {
		sum = sum[:8]
	}
	return appVersion + "-" + sum
}

// serverRoot holds one directory per deployed build, below $HOME.
const serverRoot = "/.cache/agent-sessions/server/"

// RemoteServerPath is the installed binary for one version tag.
func RemoteServerPath(home, versionTag string) string {
	return strings.TrimRight(home, "/") + serverRoot + versionTag + "/agent-sessions-cli"
}

// buildDir is the build directory of a binary at RemoteServerPath.
func buildDir(bin string) (string, bool) {
	dir, ok := strings.CutSuffix(bin, "/agent-sessions-cli")
	if !ok || !strings.Contains(dir, serverRoot) {
		return "", false
	}
	return dir, true
}

// PruneAfter is how long a build must go unstarted before a deploy removes
// it. Every start touches its build directory, so a build some client still
// uses is never removed.
const PruneAfter = 30 * 24 * time.Hour

// DeployServer streams a bundled server archive to the remote host (see
// unpackScript), checks `version` output, and prunes older builds. localGzPath may be empty to locate the bundle for probe.OS/Arch.
// It returns the absolute remote path of the installed binary.
func DeployServer(ctx context.Context, alias string, opts SSHOptions, probe *HostProbe, localGzPath string) (string, error) {
	// Setup uses separate SSH authentications on Windows, without a mux master.
	// Bound its upload/verification sequence, not the persistent session or
	// Unix setup, which may include interactive credential prompts.
	if !SupportsSSHAskpass() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}
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
	targetDir := strings.TrimSuffix(targetBin, "/agent-sessions-cli")

	gz, err := os.Open(localGzPath)
	if err != nil {
		return "", fmt.Errorf("open server bundle: %w", err)
	}
	defer func() { _ = gz.Close() }()

	unpack := unpackScript(targetDir, targetBin, sum, rpc.GenerateNonce()[:8])
	if err := runRemote(ctx, alias, opts, nil, gz, unpack); err != nil {
		return "", fmt.Errorf("deploy server: %w", err)
	}

	if err := verifyRemoteVersion(ctx, alias, opts, targetBin); err != nil {
		return "", err
	}

	// Best-effort: remove builds no client has started for PruneAfter. A
	// build that is running keeps working without its directory, and the
	// next start of that version deploys it again.
	prune := pruneScript(strings.TrimSuffix(targetDir, "/"+tag), tag)
	_ = runRemote(ctx, alias, opts, nil, nil, prune)

	return targetBin, nil
}

// pruneScript removes the build directories in root, other than keep, that
// have not been touched for PruneAfter.
func pruneScript(root, keep string) string {
	return fmt.Sprintf(
		`cd %[1]s 2>/dev/null && find . -mindepth 1 -maxdepth 1 -type d ! -name %[2]s -mtime +%[3]d -exec rm -rf -- {} + 2>/dev/null; true`,
		QuotePOSIX(root),
		QuotePOSIX(keep),
		int(PruneAfter/(24*time.Hour)),
	)
}

// unpackScript reads the gzip archive on stdin into targetDir and installs it
// as targetBin. It checks the archive against sum, the sha256 of the same .gz
// bytes, before unpacking. The hash is read from stdin so no filename is
// echoed or re-quoted. macOS has shasum rather than sha256sum; with neither,
// the check is skipped (minimal images) and the version check still runs.
// umask 077 keeps every directory it creates private. A mismatch exits 86.
// Temp files carry token, so two clients deploying the same build at once do
// not write into each other's files; the last rename wins with equal bytes.
// Temp files an interrupted deploy left behind are removed once they are
// older than any deploy still writing could be.
func unpackScript(targetDir, targetBin, sum, token string) string {
	tmpBin := targetBin + ".tmp-" + token
	return fmt.Sprintf(
		`umask 077 && mkdir -p %[1]s && chmod 700 %[1]s && `+
			`{ find %[1]s -maxdepth 1 -name 'agent-sessions-cli.tmp-*' -mmin +60 -exec rm -f -- {} + 2>/dev/null; true; } && `+
			`rm -f %[2]s %[3]s && cat > %[2]s && `+
			`if command -v sha256sum >/dev/null 2>&1; then h=$(sha256sum < %[2]s); `+
			`elif command -v shasum >/dev/null 2>&1; then h=$(shasum -a 256 < %[2]s); else h=; fi && `+
			`case "$h" in ''|%[4]s*) ;; *) rm -f %[2]s; echo 'server bundle checksum mismatch' >&2; exit 86;; esac && `+
			`gzip -dc < %[2]s > %[3]s && rm -f %[2]s && chmod 700 %[3]s && mv -f %[3]s %[5]s`,
		QuotePOSIX(targetDir),
		QuotePOSIX(tmpBin+".gz"),
		QuotePOSIX(tmpBin),
		QuotePOSIX(sum), // quoted in a case pattern: matched literally
		QuotePOSIX(targetBin),
	)
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
		return fmt.Errorf("%w: deployed server is %q, app is %q", ErrServerVersionMismatch, got, want)
	}
	return nil
}

// runRemote executes script inside a login shell on alias. stdin, when set,
// is the ssh process stdin (used to stream the gzip archive).
func runRemote(ctx context.Context, alias string, opts SSHOptions, stdout io.Writer, stdin io.Reader, script string) error {
	opts.NoTTY = true
	cmd, err := BuildSSHCmd(ctx, alias, []string{LoginShell, "-lc", script}, opts)
	if err != nil {
		return err
	}
	cmd.Stdin = stdin
	if stdout != nil {
		cmd.Stdout = stdout
	}
	var stderr sshStderr
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return sshFailure(err, stderr.String())
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
