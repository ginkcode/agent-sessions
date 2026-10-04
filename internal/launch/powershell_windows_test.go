//go:build windows

package launch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// With LAUNCH_FAKE_AGENT set, the test binary acts as an agent CLI: it
// records its arguments, working directory and whether its stdin is a
// console, and exits.
func TestMain(m *testing.M) {
	if out := os.Getenv("LAUNCH_FAKE_AGENT"); out != "" {
		wd, _ := os.Getwd()
		var mode uint32
		console := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
		data, _ := json.Marshal(map[string]any{"args": os.Args[1:], "wd": wd, "console": console})
		_ = os.WriteFile(out, data, 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeRun struct {
	Args    []string `json:"args"`
	WD      string   `json:"wd"`
	Console bool     `json:"console"`
}

// runScript runs a generated line the way the console does, but without a
// window or the user's profile, and returns what the fake agent saw.
func runScript(t *testing.T, script string) (fakeRun, bool) {
	t.Helper()
	ps, err := windowsPowerShell()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "run.json")
	cmd := exec.Command(ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "LAUNCH_FAKE_AGENT="+out)
	output, runErr := cmd.CombinedOutput()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Logf("powershell: %v\n%s", runErr, output)
		return fakeRun{}, false
	}
	var r fakeRun
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r, true
}

// sameDir reports whether two paths name one directory. String comparison
// is not enough: TEMP may hold an 8.3 short name (RUNNER~1) while the agent
// sees the long one.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}

// trickyDir makes a directory whose name PowerShell would mangle if quoting
// were wrong.
func trickyDir(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "work dir 'q' \u2019s $HOME `t; @a,b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeAgent copies the test binary into a directory with an awkward name.
func fakeAgent(t *testing.T) string {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(trickyDir(t), "fake agent.exe")
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

var trickyArgs = []string{
	"--resume",
	"ses_01ABC",
	`Read C:\Users\me\AppData\Local\agent-sessions\handoffs\s-handoff.md completely to restore the context of an earlier session, then follow its instructions and wait for my next request.`,
	"it's \u2018q\u2019 $env:PATH `n @a,b;c & | < > ^ !x!",
	`C:\trailing\`,
}

func TestPowerShellScriptPassesArgsExactly(t *testing.T) {
	exe := fakeAgent(t)
	dir := trickyDir(t)
	script := PowerShellCommand(append([]string{exe}, trickyArgs...), dir)
	r, ok := runScript(t, script)
	if !ok {
		t.Fatalf("agent did not run; script: %s", script)
	}
	if !reflect.DeepEqual(r.Args, trickyArgs) {
		t.Errorf("args =\n %q\nwant\n %q", r.Args, trickyArgs)
	}
	if !sameDir(t, r.WD, dir) {
		t.Errorf("wd = %q, want %q", r.WD, dir)
	}
}

func TestPowerShellScriptThroughCmdShim(t *testing.T) {
	exe := fakeAgent(t)
	shim := filepath.Join(filepath.Dir(exe), "agent.cmd")
	// The shape of an npm shim: forward every argument to the real program.
	if err := os.WriteFile(shim, []byte("@\"%~dp0fake agent.exe\" %*\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	args := trickyArgs[:4]
	if err := ValidateArgs(shim, args); err != nil {
		t.Fatal(err)
	}
	dir := trickyDir(t)
	r, ok := runScript(t, PowerShellCommand(append([]string{shim}, args...), dir))
	if !ok {
		t.Fatal("agent did not run through the shim")
	}
	if !reflect.DeepEqual(r.Args, args) {
		t.Errorf("args =\n %q\nwant\n %q", r.Args, args)
	}
}

func TestPowerShellScriptStopsOnMissingDir(t *testing.T) {
	exe := fakeAgent(t)
	missing := filepath.Join(t.TempDir(), "gone")
	if _, ran := runScript(t, PowerShellCommand([]string{exe, "x"}, missing)); ran {
		t.Fatal("agent ran although Set-Location failed")
	}
}

// TestConsoleGivesAgentTheConsole starts the agent the way Open in terminal
// does, minus the visible window, and checks that the agent reads the
// console. With NUL as stdin, PowerShell exits at once despite -NoExit.
func TestConsoleGivesAgentTheConsole(t *testing.T) {
	exe := fakeAgent(t)
	dir := trickyDir(t)
	out := filepath.Join(t.TempDir(), "run.json")
	t.Setenv("LAUNCH_FAKE_AGENT", out)
	script := PowerShellCommand([]string{exe, "--resume", "s1"}, dir) + "; exit"
	h, err := startConsole(script, dir, windows.CREATE_NO_WINDOW)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if ev, _ := windows.WaitForSingleObject(h, 30000); ev != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(h, 1)
		t.Fatal("PowerShell did not finish")
	}
	var r fakeRun
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		data, err := os.ReadFile(out)
		if err == nil && json.Unmarshal(data, &r) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not run")
		}
	}
	if !r.Console {
		t.Error("agent stdin is not the console")
	}
	if !reflect.DeepEqual(r.Args, []string{"--resume", "s1"}) || !sameDir(t, r.WD, dir) {
		t.Errorf("run = %+v", r)
	}
}

// TestFindOnThisMachine logs which CLIs Find resolves here, without running
// them. Opt in with AGENT_SESSIONS_FIND_SMOKE=1.
func TestFindOnThisMachine(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_FIND_SMOKE") == "" {
		t.Skip("set AGENT_SESSIONS_FIND_SMOKE=1 to resolve the installed agent CLIs")
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		exe, err := Find(name)
		t.Logf("%s: %+v err=%v", name, exe, err)
	}
}
