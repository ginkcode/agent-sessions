//go:build windows

package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWSLConsoleSmoke starts a script in a real distribution the way Open in
// terminal does, minus the visible window. Opt in with
// AGENT_SESSIONS_WSL_SMOKE=<distro>.
func TestWSLConsoleSmoke(t *testing.T) {
	distro := os.Getenv("AGENT_SESSIONS_WSL_SMOKE")
	if distro == "" {
		t.Skip("set AGENT_SESSIONS_WSL_SMOKE=<distro> to run against WSL")
	}
	wsl, err := WSLBinary()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.txt")
	arg := "it's \"q\" $HOME `id` back\\slash ünï 100%"
	script := "cd /tmp || exit 1\nprintf '%s|%s' \"$PWD\" " + ShellEscape(arg) + " > \"$(wslpath -u " + ShellEscape(out) + ")\"\n"
	h, err := startProcess(wsl, append([]string{wsl}, WSLExecArgs(distro, script)...), t.TempDir(), windows.CREATE_NO_WINDOW)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if ev, _ := windows.WaitForSingleObject(h, 60000); ev != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(h, 1)
		t.Fatal("wsl.exe did not finish")
	}
	var got []byte
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if got, err = os.ReadFile(out); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("script did not run")
		}
	}
	if want := "/tmp|" + arg; strings.TrimSpace(string(got)) != want {
		t.Errorf("script wrote %q, want %q", got, want)
	}
}
