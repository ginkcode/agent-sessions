package remote

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSSHWindowsPolicy(t *testing.T) {
	args, err := buildSSHArgs("box", nil, SSHOptions{
		ControlMaster: "yes", ControlPath: "ignored", ControlPersist: "10m", ForceTTY: true,
		ExtraOptions: []string{"BatchMode=no", "ControlMaster=auto", "ControlPath=bad", "ControlPersist=yes"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	// OpenSSH uses the first value; later options and config cannot re-enable
	// prompts or multiplexing. No shell or TTY is involved.
	first := make(map[string]string)
	for i, arg := range args {
		if arg == "-t" {
			t.Fatal("Windows forced a TTY")
		}
		if arg == "-o" {
			key, value, _ := strings.Cut(args[i+1], "=")
			if _, ok := first[key]; !ok {
				first[key] = value
			}
		}
	}
	for key, want := range map[string]string{"BatchMode": "yes", "ControlMaster": "no", "ControlPath": "none", "ControlPersist": "no"} {
		if first[key] != want {
			t.Errorf("%s=%q, want %q", key, first[key], want)
		}
	}
}

func TestSSHWindowsEnvironment(t *testing.T) {
	for _, key := range []string{"SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "AGENT_SESSIONS_ASKPASS_SOCK", "AGENT_SESSIONS_ASKPASS_TOKEN"} {
		t.Setenv(key, "private-test-value")
	}
	t.Setenv("SSH_AUTH_SOCK", "agent-test-socket")
	t.Setenv("DISPLAY", "")
	env := strings.Join(sshEnvironment(SSHOptions{AskpassBinary: "ignored", AskpassSock: "ignored", AskpassToken: "ignored"}, true), "\n")
	if strings.Contains(env, "private-test-value") || strings.Contains(env, "ignored") || strings.Contains(env, "dummy:0") {
		t.Fatal("Windows inherited or configured askpass credentials")
	}
	if !strings.Contains(env, "SSH_ASKPASS_REQUIRE=never") || !strings.Contains(env, "SSH_AUTH_SOCK=agent-test-socket") {
		t.Fatal("Windows must disable prompts but preserve the agent")
	}
}

func TestSSHProbeFailures(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		want   error
		detail string
	}{
		{"auth-fail", ErrSSHAuthentication, "Permission denied (publickey)"},
		{"hostkey-fail", ErrSSHHostKey, "Host key verification failed"},
		{"network-fail", nil, "Connection refused"},
		{"large-stderr", nil, "last diagnostic"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			bin := fakeSSHPath(t, tc.mode, nil)
			_, err := StartSession(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, nil, nil)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("failure = %v, want %v with %s", err, tc.want, tc.detail)
			}
			if len(err.Error()) > stderrCap+200 {
				t.Fatal("unbounded diagnostics")
			}
		})
	}
}

func TestSSHProbeTimeoutAndCancellation(t *testing.T) {
	bin := fakeSSHPath(t, "hang", nil)
	start := time.Now()
	_, err := probeHost(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, "dev", 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "waiting for SSH peer") || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout = %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ProbeHost(ctx, "box", SSHOptions{Binary: bin, ControlMaster: "no"}, "dev")
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestSSHProbeAllowsUnixPrompts(t *testing.T) {
	if !SupportsSSHAskpass() {
		t.Skip("Windows requires noninteractive authentication")
	}
	bin := fakeSSHPath(t, "probe-slow", nil)
	opts := SSHOptions{Binary: bin, ControlMaster: "no"}
	if _, err := probeHost(context.Background(), "box", opts, "dev", 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded noninteractive probe = %v", err)
	}
	if _, err := ProbeHost(context.Background(), "box", opts, "dev"); err != nil {
		t.Fatalf("Unix probe must allow credential prompts: %v", err)
	}
}

func TestSSHRemotePermissionFailureIsNotAuth(t *testing.T) {
	bin := fakeSSHPath(t, "remote-denied", nil)
	err := runRemote(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, nil, nil, "mkdir test")
	if err == nil || errors.Is(err, ErrSSHAuthentication) || !strings.Contains(err.Error(), "mkdir: Permission denied") {
		t.Fatalf("misclassified remote failure: %v", err)
	}
}

func TestSSHExplicitBinary(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := BuildSSHCmd(context.Background(), "box", nil, SSHOptions{Binary: bin, ControlMaster: "no"})
	if err != nil || cmd.Path != bin || cmd.WaitDelay == 0 {
		t.Fatalf("explicit binary ignored: %v, %v", cmd, err)
	}
}
