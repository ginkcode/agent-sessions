//go:build windows

package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsSSHNativeClientAndHiddenProcess(t *testing.T) {
	// Discovery may legitimately fail on a Windows install without OpenSSH.
	bin, err := defaultSSHBinary()
	if err != nil {
		if !errors.Is(err, ErrSSHClientNotFound) {
			t.Fatal(err)
		}
		return
	}
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(bin, filepath.Join(dir, "OpenSSH", "ssh.exe")) {
		t.Fatalf("not the native SSH client: %q", bin)
	}
	cmd, err := BuildSSHCmd(context.Background(), "box", nil, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != bin || cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("SSH spawn not hidden/native: %q, %+v", cmd.Path, cmd.SysProcAttr)
	}
	// A synthetic child confirms there is no visible console window. No network.
	fake := fakeSSHPath(t, "no-console", nil)
	cmd, err = BuildSSHCmd(context.Background(), "box", nil, SSHOptions{Binary: fake})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("console suppression: %v, %s", err, out)
	}
}

func TestWindowsSSHAskpassUnavailable(t *testing.T) {
	if broker, err := NewAskpassBroker(nil); broker != nil || !errors.Is(err, ErrSSHAskpassUnsupported) {
		if broker != nil {
			_ = broker.Close()
		}
		t.Fatalf("Windows must not create a Unix credential listener: %v", err)
	}
	if err := RunAskpassHelper(context.Background(), "unused", "unused", "unused", io.Discard, io.Discard); !errors.Is(err, ErrSSHAskpassUnsupported) {
		t.Fatalf("Windows helper tried to use a Unix credential socket: %v", err)
	}
	if err := StopControlMaster(context.Background(), filepath.Join(t.TempDir(), "nonexistent.exe"), "box", SSHOptions{}); err != nil {
		t.Fatalf("Windows tried to stop a master: %v", err)
	}
}

// Opt-in read-only native SSH check. No deploy, server start, scan or transcript
// read; host keys are neither accepted automatically nor updated.
func TestWindowsSSHMetadataSmoke(t *testing.T) {
	host := os.Getenv("AGENT_SESSIONS_SSH_SMOKE")
	if host == "" {
		t.Skip("set AGENT_SESSIONS_SSH_SMOKE to a host alias for read-only uname QA")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, err := BuildSSHCmd(ctx, host, []string{"uname", "-sm"}, SSHOptions{
		ExtraOptions: []string{"ConnectTimeout=10", "UpdateHostKeys=no"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native SSH metadata check: %v, %s", err, out)
	}
	t.Logf("native SSH metadata: %s", strings.TrimSpace(string(out)))
}

func TestWindowsSSHDefaultFlags(t *testing.T) {
	// A long runtime directory must never be created for a control socket.
	dir := filepath.Join(t.TempDir(), strings.Repeat("a", 50))
	t.Setenv("XDG_RUNTIME_DIR", dir)
	args, err := BuildSSHArgs("box", nil, SSHOptions{ControlMaster: "auto", ForceTTY: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, opt := range []string{"BatchMode=yes", "ControlMaster=no", "ControlPath=none", "ControlPersist=no"} {
		if !strings.Contains(strings.Join(args, " "), opt) {
			t.Errorf("missing %s in %v", opt, args)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("created a Unix control directory: %v", err)
	}
}
