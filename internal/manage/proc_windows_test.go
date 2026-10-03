//go:build windows

package manage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Reads only this test process's own command line.
func TestWindowsArgsReadsOwnCommandLine(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Base(exe)
	argv, err := (windowsImageProcFS{}).Args(os.Getpid(), image)
	if err != nil || !reflect.DeepEqual(argv[1:], os.Args[1:]) {
		t.Fatalf("Args = %q, %v; want %q", argv, err, os.Args)
	}
	// A reused PID now running another executable must not be trusted.
	if _, err := (windowsImageProcFS{}).Args(os.Getpid(), "opencode-cli.exe"); !errors.Is(err, ErrProcessUnknown) {
		t.Fatalf("image mismatch accepted: %v", err)
	}
	if _, err := (windowsImageProcFS{}).Args(0, "System Idle Process"); !errors.Is(err, ErrProcessUnknown) {
		t.Fatalf("unreadable process accepted: %v", err)
	}
}

func TestDecodeWindowsCommandLine(t *testing.T) {
	cmd := []uint16{}
	for _, r := range `"C:\Program Files\OpenCode\opencode-cli.exe" serve --hostname "127.0.0.1"` {
		cmd = append(cmd, uint16(r))
	}
	argv, err := decodeWindowsCommandLine(append(cmd, 0))
	want := []string{`C:\Program Files\OpenCode\opencode-cli.exe`, "serve", "--hostname", "127.0.0.1"}
	if err != nil || !reflect.DeepEqual(argv, want) {
		t.Fatalf("decode = %q, %v", argv, err)
	}
}

// Opt-in: reports the guard's verdict for this machine's processes. It logs
// only agent names and executable names, never arguments.
func TestWindowsProcessGuardOnThisMachine(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_PROC_SMOKE") != "1" {
		t.Skip("set AGENT_SESSIONS_PROC_SMOKE=1 to inspect this machine")
	}
	live, err := newLiveGuard(windowsImageProcFS{}).procLive(t.Context())
	t.Logf("blocking agents: %v, error: %v", live, err)
}
