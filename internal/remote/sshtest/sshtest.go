// Package sshtest runs a throwaway OpenSSH server in a Docker container for
// the opt-in SSH integration tests. It is imported by tests only.
//
// Tests using it are skipped unless AGENT_SESSIONS_SSH_DOCKER=1 (see
// `make test-ssh`). The container listens on a random 127.0.0.1 port, trusts
// one generated client key, and presents a generated host key pinned in a
// temporary known_hosts, so strict host key checking stays on and nothing in
// the user's ~/.ssh is read or written.
package sshtest

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// EnableEnv must be "1" for Start to run instead of skipping the test.
const EnableEnv = "AGENT_SESSIONS_SSH_DOCKER"

const image = "agent-sessions-sshd:test"

// Remote users, one per login shell the app must cope with. Both print a
// banner from their login profile, which the nonce preface has to skip.
const (
	UserSh  = "tester" // /bin/sh (BusyBox ash)
	UserZsh = "zuser"  // /bin/zsh, as on macOS
)

// Banner is printed by every login shell in the container.
const Banner = "Welcome to the agent-sessions test host"

const dockerfile = `FROM alpine:3.20
RUN apk add --no-cache openssh-server zsh && \
    adduser -D -s /bin/sh ` + UserSh + ` && \
    adduser -D -s /bin/zsh ` + UserZsh + ` && \
    for u in ` + UserSh + ` ` + UserZsh + `; do \
      echo "$u:$(head -c 18 /dev/urandom | base64)" | chpasswd; \
    done && \
    printf '%s\n' \
      'PasswordAuthentication no' \
      'KbdInteractiveAuthentication no' \
      'PermitRootLogin no' \
      'AllowAgentForwarding no' \
      'HostKey /etc/ssh/test_host_key' > /etc/ssh/sshd_config.d/test.conf
CMD ["sleep", "infinity"]
`

// Host is one running container.
type Host struct {
	Container   string
	ConfigFile  string // pass as SSHOptions.ConfigFile
	ControlPath string // pass as SSHOptions.ControlPath
	// Aliases in ConfigFile, keyed by remote user.
	Aliases map[string]string
}

// Alias is the ssh alias for user.
func (h *Host) Alias(user string) string { return h.Aliases[user] }

var (
	imageOnce sync.Once
	imageErr  error
)

// Start runs a fresh container and waits until sshd accepts the client key.
// The container, keys and control sockets are removed when the test ends.
func Start(t testing.TB) *Host {
	t.Helper()
	if os.Getenv(EnableEnv) != "1" {
		t.Skipf("set %s=1 to run the Docker sshd tests", EnableEnv)
	}
	for _, bin := range []string{"docker", "ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not found: %v", bin, err)
		}
	}
	imageOnce.Do(func() {
		cmd := exec.Command("docker", "build", "-q", "-t", image, "-")
		cmd.Stdin = strings.NewReader(dockerfile)
		if out, err := cmd.CombinedOutput(); err != nil {
			imageErr = fmt.Errorf("docker build: %v: %s", err, out)
		}
	})
	if imageErr != nil {
		t.Fatal(imageErr)
	}

	dir := t.TempDir()
	hostKey := filepath.Join(dir, "host_key")
	clientKey := filepath.Join(dir, "client_key")
	keygen(t, hostKey)
	keygen(t, clientKey)

	name := fmt.Sprintf("as-sshd-%d-%d", os.Getpid(), time.Now().UnixNano())
	run(t, "docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1::22", image)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	h := &Host{Container: name, Aliases: map[string]string{}}
	h.cp(t, hostKey, "/etc/ssh/test_host_key")
	h.Exec(t, "chown root:root /etc/ssh/test_host_key && chmod 600 /etc/ssh/test_host_key")
	for _, user := range []string{UserSh, UserZsh} {
		h.cp(t, clientKey+".pub", "/tmp/"+user+".pub")
		profile := ".profile"
		if user == UserZsh {
			profile = ".zprofile"
		}
		h.Exec(t, fmt.Sprintf(
			`set -e; home=/home/%[1]s; mkdir -p $home/.ssh; mv /tmp/%[1]s.pub $home/.ssh/authorized_keys; `+
				`echo 'echo "%[2]s"' > $home/%[3]s; `+
				`chown -R %[1]s:%[1]s $home; chmod 700 $home/.ssh; chmod 600 $home/.ssh/authorized_keys`,
			user, Banner, profile))
	}
	h.Exec(t, "/usr/sbin/sshd -e")

	port := strings.TrimSpace(run(t, "docker", "port", name, "22/tcp"))
	if i := strings.LastIndex(port, ":"); i >= 0 {
		port = port[i+1:]
	}
	pub, err := os.ReadFile(hostKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(pub))
	knownHosts := filepath.Join(dir, "known_hosts")
	writeFile(t, knownHosts, fmt.Sprintf("[127.0.0.1]:%s %s %s\n", port, fields[0], fields[1]))

	var cfg strings.Builder
	for _, user := range []string{UserSh, UserZsh} {
		alias := "as-test-" + user
		h.Aliases[user] = alias
		fmt.Fprintf(&cfg, "Host %s\n  User %s\n", alias, user)
	}
	fmt.Fprintf(&cfg, `Host *
  HostName 127.0.0.1
  Port %s
  IdentityFile %s
  IdentitiesOnly yes
  IdentityAgent none
  UserKnownHostsFile %s
  GlobalKnownHostsFile /dev/null
  StrictHostKeyChecking yes
  PasswordAuthentication no
  LogLevel ERROR
`, port, clientKey, knownHosts)
	h.ConfigFile = filepath.Join(dir, "ssh_config")
	writeFile(t, h.ConfigFile, cfg.String())

	// Unix socket paths are short-limited, so keep the control dir out of
	// the long t.TempDir path.
	cmDir, err := os.MkdirTemp("", "as-cm")
	if err != nil {
		t.Fatal(err)
	}
	h.ControlPath = filepath.Join(cmDir, "%C")
	t.Cleanup(func() {
		for _, alias := range h.Aliases {
			_ = exec.Command("ssh", "-F", h.ConfigFile, "-O", "exit", "-o", "ControlPath="+h.ControlPath, "--", alias).Run()
		}
		_ = os.RemoveAll(cmDir)
	})

	h.waitReady(t)
	return h
}

// waitReady retries a plain login until sshd is up.
func (h *Host) waitReady(t testing.TB) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		out, err := exec.Command("ssh", "-F", h.ConfigFile, "-o", "BatchMode=yes", "--", h.Alias(UserSh), "true").CombinedOutput()
		if err == nil {
			return
		}
		last = out
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("sshd did not accept the client key: %s", last)
}

// Exec runs a shell script in the container as root and returns stdout.
func (h *Host) Exec(t testing.TB, script string) string {
	t.Helper()
	return run(t, "docker", "exec", h.Container, "sh", "-c", script)
}

// ExecAs runs a shell script in the container as user and returns stdout.
func (h *Host) ExecAs(t testing.TB, user, script string) string {
	t.Helper()
	return run(t, "docker", "exec", "-u", user, "-w", "/home/"+user, "-e", "HOME=/home/"+user, h.Container, "sh", "-c", script)
}

// CopyTree copies a local directory to /home/<user>/<rel> in the container,
// owned by user.
func (h *Host) CopyTree(t testing.TB, localDir, user, rel string) {
	t.Helper()
	dst := "/home/" + user + "/" + rel
	h.Exec(t, fmt.Sprintf("mkdir -p '%s'", filepath.Dir(dst)))
	run(t, "docker", "cp", localDir, h.Container+":"+dst)
	h.Exec(t, fmt.Sprintf("chown -R %[1]s:%[1]s /home/%[1]s", user))
}

func (h *Host) cp(t testing.TB, local, remote string) {
	t.Helper()
	run(t, "docker", "cp", local, h.Container+":"+remote)
}

type bundle struct {
	once sync.Once
	dir  string
	err  error
}

var (
	bundlesMu sync.Mutex
	bundles   = map[string]*bundle{}
)

// ServerBundleDir builds agent-sessions-cli for linux/<host arch>, stamped
// with version so the deploy version check passes, gzips it the way
// `make remote-servers` does, and returns the directory holding it. Point
// AGENT_SESSIONS_REMOTE_SERVERS_DIR at it. Each version builds once per
// process.
func ServerBundleDir(t testing.TB, version string) string {
	t.Helper()
	bundlesMu.Lock()
	b := bundles[version]
	if b == nil {
		b = &bundle{}
		bundles[version] = b
	}
	bundlesMu.Unlock()
	b.once.Do(func() {
		b.dir, b.err = buildBundle(version)
	})
	if b.err != nil {
		t.Fatal(b.err)
	}
	return b.dir
}

func buildBundle(version string) (string, error) {
	root, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("find module root: %w", err)
	}
	// A fixed name per version: reruns overwrite it instead of piling up.
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("agent-sessions-sshtest-%d-%s", os.Getuid(), version))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Test packages run as parallel processes sharing dir: build into
	// private temp files and rename the finished archive into place.
	name := "agent-sessions-cli-linux-" + runtime.GOARCH
	bin, err := os.CreateTemp(dir, name+".build-*")
	if err != nil {
		return "", err
	}
	_ = bin.Close()
	defer os.Remove(bin.Name())
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-X github.com/ginkcode/agent-sessions/internal/version.Version="+version,
		"-o", bin.Name(), "./cmd/agent-sessions-cli")
	cmd.Dir = strings.TrimSpace(string(root))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build server: %v: %s", err, out)
	}
	src, err := os.Open(bin.Name())
	if err != nil {
		return "", err
	}
	defer src.Close()
	gz, err := os.CreateTemp(dir, name+".gz-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(gz.Name())
	zw := gzip.NewWriter(gz)
	if _, err := io.Copy(zw, src); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := zw.Close(); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(gz.Name(), 0o644); err != nil {
		return "", err
	}
	return dir, os.Rename(gz.Name(), filepath.Join(dir, name+".gz"))
}

func keygen(t testing.TB, path string) {
	t.Helper()
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "agent-sessions-test", "-f", path)
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func run(t testing.TB, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}
