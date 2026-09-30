package remote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBuildSSHArgs_Basic(t *testing.T) {
	opts := SSHOptions{
		ControlPath: "/tmp/test-cp-%C",
		NoTTY:       true,
	}

	args, err := BuildSSHArgs("prod-box", []string{"echo", "hello world"}, opts)
	if err != nil {
		t.Fatalf("BuildSSHArgs failed: %v", err)
	}

	// Verify key flags
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "BatchMode=no") {
		t.Errorf("missing BatchMode=no in %s", joined)
	}
	if !strings.Contains(joined, "ControlMaster=auto") {
		t.Errorf("missing ControlMaster=auto in %s", joined)
	}
	if !strings.Contains(joined, "ControlPath=/tmp/test-cp-%C") {
		t.Errorf("missing ControlPath in %s", joined)
	}
	if !strings.Contains(joined, "-T") {
		t.Errorf("missing -T in %s", joined)
	}

	// Verify delimiter before alias
	foundDelimiter := false
	for i, arg := range args {
		if arg == "--" {
			foundDelimiter = true
			if i+1 >= len(args) || args[i+1] != "prod-box" {
				t.Fatalf("expected 'prod-box' immediately after '--', got: %v", args)
			}
			break
		}
	}
	if !foundDelimiter {
		t.Fatalf("missing '--' delimiter before alias in %v", args)
	}

	// Verify remote command was quoted at the end
	lastArg := args[len(args)-1]
	if lastArg != "'echo' 'hello world'" {
		t.Errorf("expected quoted command, got: %q", lastArg)
	}
}

func TestBuildSSHArgs_FlagInjection(t *testing.T) {
	malicious := []string{
		"-oProxyCommand=evil",
		"-F/etc/evil",
		"-v",
		"--version",
	}

	for _, alias := range malicious {
		_, err := BuildSSHArgs(alias, []string{"ls"}, SSHOptions{})
		if !errors.Is(err, ErrInvalidHostAlias) {
			t.Errorf("expected %q to be rejected, got: %v", alias, err)
		}
	}
}

func TestBuildSSHArgs_Options(t *testing.T) {
	opts := SSHOptions{
		ConfigFile:          "/path/to/ssh.conf",
		ServerAliveInterval: 30,
		ServerAliveCountMax: 5,
		ForceTTY:            true,
		ControlMaster:       "no",
		ExtraOptions:        []string{"StrictHostKeyChecking=ask"},
	}

	args, err := BuildSSHArgs("staging", nil, opts)
	if err != nil {
		t.Fatalf("BuildSSHArgs failed: %v", err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-F /path/to/ssh.conf") {
		t.Errorf("missing -F in %s", joined)
	}
	if !strings.Contains(joined, "ServerAliveInterval=30") {
		t.Errorf("missing ServerAliveInterval=30 in %s", joined)
	}
	if !strings.Contains(joined, "-t") {
		t.Errorf("missing -t in %s", joined)
	}
	if strings.Contains(joined, "ControlMaster") {
		t.Errorf("expected no ControlMaster flags when ControlMaster='no', got: %s", joined)
	}
	if !strings.Contains(joined, "StrictHostKeyChecking=ask") {
		t.Errorf("missing extra option in %s", joined)
	}
}

func TestBuildSSHCmd_Environment(t *testing.T) {
	opts := SSHOptions{
		AskpassBinary: "/usr/bin/my-askpass",
		AskpassSock:   "/tmp/as.sock",
		AskpassToken:  "secret-tok-123",
	}

	cmd, err := BuildSSHCmd(context.Background(), "myhost", []string{"true"}, opts)
	if err != nil {
		t.Fatalf("BuildSSHCmd failed: %v", err)
	}

	envMap := make(map[string]string)
	for _, env := range cmd.Env {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if envMap["SSH_ASKPASS"] != "/usr/bin/my-askpass" {
		t.Errorf("SSH_ASKPASS = %q", envMap["SSH_ASKPASS"])
	}
	if envMap["SSH_ASKPASS_REQUIRE"] != "force" {
		t.Errorf("SSH_ASKPASS_REQUIRE = %q", envMap["SSH_ASKPASS_REQUIRE"])
	}
	if envMap["AGENT_SESSIONS_ASKPASS_SOCK"] != "/tmp/as.sock" {
		t.Errorf("AGENT_SESSIONS_ASKPASS_SOCK = %q", envMap["AGENT_SESSIONS_ASKPASS_SOCK"])
	}
	if envMap["AGENT_SESSIONS_ASKPASS_TOKEN"] != "secret-tok-123" {
		t.Errorf("AGENT_SESSIONS_ASKPASS_TOKEN = %q", envMap["AGENT_SESSIONS_ASKPASS_TOKEN"])
	}
	if envMap["DISPLAY"] == "" {
		t.Errorf("expected non-empty DISPLAY")
	}
}

func TestControlDir_Permissions(t *testing.T) {
	dir, err := ControlDir()
	if err != nil {
		t.Fatalf("ControlDir failed: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("expected 0700 permissions on control dir, got: %o", perm)
	}
}

// The remote command line is run by the remote user's shell, as sshd does
// with `$SHELL -c <line>`. LoginShell must expand there; the script must not.
func TestBuildSSHArgs_LoginShellExpands(t *testing.T) {
	args, err := BuildSSHArgs("box", []string{LoginShell, "-lc", `printf '%s|' "$0" '$(whoami)'`}, SSHOptions{ControlMaster: "no"})
	if err != nil {
		t.Fatal(err)
	}
	line := args[len(args)-1]
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %q: %v: %s", line, err, out)
	}
	if got := string(out); got != "/bin/sh|$(whoami)|" {
		t.Fatalf("output = %q (line %q)", got, line)
	}
}
