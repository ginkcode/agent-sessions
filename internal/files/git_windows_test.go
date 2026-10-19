//go:build windows

package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Relaunch as a console-less GUI, then as its fake git.exe child. This tests
// the actual runGit path rather than just inspecting process creation flags.
func TestMain(m *testing.M) {
	if os.Getenv("FILES_FAKE_GIT") == "1" {
		if hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call(); hwnd != 0 {
			_, _ = os.Stdout.WriteString("window")
		} else {
			_, _ = os.Stdout.WriteString("hidden")
		}
		os.Exit(0)
	}
	if out := os.Getenv("FILES_FAKE_GUI"); out != "" {
		_ = os.Setenv("FILES_FAKE_GIT", "1")
		result, ok := runGit(context.Background(), os.TempDir(), "diff", "--no-color", "HEAD", "--", "Makefile")
		if !ok {
			os.Exit(1)
		}
		if err := os.WriteFile(out, result, 0o600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestGitHidesConsole(t *testing.T) {
	cmd := exec.Command("git", "--version")
	hideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("Git console not suppressed: %+v", cmd.SysProcAttr)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git.exe"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	out := filepath.Join(t.TempDir(), "state")
	gui := exec.Command(self)
	gui.Env = append(os.Environ(), "FILES_FAKE_GUI="+out, "FILES_FAKE_GIT=0")
	gui.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if err := gui.Run(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "hidden" {
		t.Fatalf("Git console = %q, error %v; want hidden", got, err)
	}
}
