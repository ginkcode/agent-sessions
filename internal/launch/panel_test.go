package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func TestPanelArgvShapes(t *testing.T) {
	dir := platform.TempDir(t)
	cmd := provider.Command{Argv: []string{"codex", "resume", "abc"}, Dir: dir}
	find := func(string) (Executable, error) { return Executable{Path: "/opt/bin/codex"}, nil }

	argv, err := posixPanelArgv(cmd, find)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 3 || argv[0] != "/bin/sh" || argv[1] != "-c" {
		t.Fatalf("posix argv = %q", argv)
	}
	for _, want := range []string{"cd -- " + ShellEscape(dir), "/opt/bin/codex resume abc", "exit \"$s\""} {
		if !strings.Contains(argv[2], want) {
			t.Errorf("script lacks %q:\n%s", want, argv[2])
		}
	}
	// The tab ends with the agent; no shell takes over.
	if strings.Contains(argv[2], "exec ") {
		t.Errorf("script starts a shell after the agent:\n%s", argv[2])
	}

	winFind := func(string) (Executable, error) { return Executable{Path: `C:\bin\codex.exe`}, nil }
	argv, err = powerShellPanelArgv(cmd, winFind, `C:\ps.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv[:3], []string{`C:\ps.exe`, "-NoLogo", "-Command"}) || len(argv) != 4 || !strings.Contains(argv[3], `C:\bin\codex.exe`) || !strings.HasSuffix(argv[3], powerShellExitStatus) {
		t.Fatalf("powershell argv = %q", argv)
	}

	for _, build := range []func(provider.Command) ([]string, error){
		func(c provider.Command) ([]string, error) { return posixPanelArgv(c, find) },
		func(c provider.Command) ([]string, error) { return powerShellPanelArgv(c, winFind, "ps") },
	} {
		if _, err := build(provider.Command{Argv: []string{"codex"}, Dir: filepath.Join(dir, "missing")}); err == nil {
			t.Error("a missing directory was accepted")
		}
		if _, err := build(provider.Command{Dir: dir}); err == nil {
			t.Error("an empty command was accepted")
		}
	}
}

func TestPosixPanelArgvRunsAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "out")
	agent := recordingAgent(t, out)
	find := func(string) (Executable, error) { return Executable{Path: agent}, nil }
	argv, err := posixPanelArgv(provider.Command{Argv: append([]string{"codex"}, posixTrickyArgs...), Dir: dir}, find)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	checkRecorded(t, out, dir, filepath.Dir(agent), posixTrickyArgs)
}

// TestPosixPanelArgvExitStatus checks that a panel tab's script reports a
// failed agent and exits with its status, so the tab shows it.
func TestPosixPanelArgvExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	agent := filepath.Join(platform.TempDir(t), "codex")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	find := func(string) (Executable, error) { return Executable{Path: agent}, nil }
	argv, err := posixPanelArgv(provider.Command{Argv: []string{"codex"}, Dir: platform.TempDir(t)}, find)
	if err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("err = %v, want exit status 3", err)
	}
	if !strings.Contains(string(b), "codex exited with status 3.") {
		t.Errorf("output = %q", b)
	}
}

func TestPanelShellArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells")
	}
	argv, err := PanelShellArgv()
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 3 || argv[0] != "/bin/sh" || argv[1] != "-c" || !strings.Contains(argv[2], "__as_shell=") {
		t.Fatalf("argv = %q", argv)
	}
}

// TestPanelShellScriptThroughSSH runs a shell tab's script as an ssh server
// would for the panel: the bootstrap argv quoted into one line, run by the
// user's login shell.
func TestPanelShellScriptThroughSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "pwd")
	shell := filepath.Join(platform.TempDir(t), "user shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n[ \"$1\" = -l ] && pwd > '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := JoinCommand(PosixBootstrapArgv(PanelShellScript(dir)))
	ran := 0
	for _, name := range []string{"sh", "bash", "zsh", "fish", "tcsh"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ran++
		_ = os.Remove(out)
		c := exec.Command(path, "-c", line)
		c.Env = append(os.Environ(), "SHELL="+shell)
		if b, err := c.CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", name, err, b)
			continue
		}
		if got, _ := os.ReadFile(out); strings.TrimSuffix(string(got), "\n") != dir {
			t.Errorf("%s: shell started in %q, want %q", name, got, dir)
		}
	}
	if ran == 0 {
		t.Skip("no shells")
	}

	// Without a directory, or with a missing one, the shell still starts.
	for _, d := range []string{"", filepath.Join(dir, "missing")} {
		_ = os.Remove(out)
		c := exec.Command("/bin/sh", "-c", PanelShellScript(d))
		c.Env = append(os.Environ(), "SHELL="+shell)
		if b, err := c.CombinedOutput(); err != nil {
			t.Errorf("dir %q: %v: %s", d, err, b)
		}
		if _, err := os.Stat(out); err != nil {
			t.Errorf("dir %q: the shell did not start", d)
		}
	}
}
