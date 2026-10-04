//go:build windows

package manage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// The test binary doubles as the processes TestRunStandaloneHidesConsole
// needs: with MANAGE_FAKE_CLI it is a provider CLI that records whether it
// has a console window; with MANAGE_FAKE_GUI it is the app, a process
// without a console, running that CLI through runStandalone.
func TestMain(m *testing.M) {
	if out := os.Getenv("MANAGE_FAKE_CLI"); out != "" {
		state := "hidden"
		if hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call(); hwnd != 0 {
			state = "window"
		}
		_ = os.WriteFile(out, []byte(state), 0o600)
		os.Exit(0)
	}
	if out := os.Getenv("MANAGE_FAKE_GUI"); out != "" {
		if err := runStandalone(context.Background(), []string{"fakecli"}, os.TempDir(), []string{"MANAGE_FAKE_CLI=" + out, "MANAGE_FAKE_GUI="}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRunStandaloneHidesConsole(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "fakecli.exe"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	out := filepath.Join(t.TempDir(), "state")

	// Like the GUI, the parent has no console: without hiding, Windows
	// would give the CLI a new, visible one.
	gui := exec.Command(self)
	gui.Env = append(os.Environ(), "MANAGE_FAKE_GUI="+out)
	gui.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if err := gui.Run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != "hidden" {
		t.Errorf("CLI console = %q, want hidden", got)
	}
}
