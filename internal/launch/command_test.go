package launch

import (
	"errors"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestPosixCommandUnchanged(t *testing.T) {
	tests := []struct {
		argv []string
		dir  string
		want string
	}{
		{[]string{"claude", "--resume", "abc"}, "/home/u/proj", "cd /home/u/proj && claude --resume abc"},
		{[]string{"codex", "Short prompt"}, "/tmp/dir", "cd /tmp/dir && codex 'Short prompt'"},
		{[]string{"claude", "Fix it"}, "", "claude 'Fix it'"},
		{[]string{"opencode", "--prompt", "it's"}, "/a b", `cd '/a b' && opencode --prompt 'it'"'"'s'`},
	}
	for _, tc := range tests {
		if got := PosixCommand(tc.argv, tc.dir); got != tc.want {
			t.Errorf("PosixCommand(%q, %q) = %q, want %q", tc.argv, tc.dir, got, tc.want)
		}
	}
}

func TestPowerShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "''"},
		{"claude", "claude"},
		{"--resume", "--resume"},
		{`C:\Users\me\proj`, `C:\Users\me\proj`},
		{"has space", "'has space'"},
		{"it's", "'it''s'"},
		{"it\u2019s", "'it\u2019\u2019s'"},
		{"\u2018a\u201ab\u201b", "'\u2018\u2018a\u201a\u201ab\u201b\u201b'"},
		{"$env:PATH", "'$env:PATH'"},
		{"a`b", "'a`b'"},
		{"@args", "'@args'"},
		{"a,b", "'a,b'"},
		{"a;b", "'a;b'"},
		{"日本", "'日本'"},
	}
	for _, tc := range tests {
		if got := PowerShellQuote(tc.in); got != tc.want {
			t.Errorf("PowerShellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPowerShellCommand(t *testing.T) {
	tests := []struct {
		argv []string
		dir  string
		want string
	}{
		{[]string{"claude", "--resume", "abc"}, `C:\work dir`, `Set-Location -LiteralPath 'C:\work dir' -ErrorAction Stop; claude --resume abc`},
		{[]string{`C:\Users\me\AppData\Local\Programs\@opencodedesktop\resources\opencode-cli.exe`, "--session", "ses_1"}, `C:\p`,
			`Set-Location -LiteralPath C:\p -ErrorAction Stop; & 'C:\Users\me\AppData\Local\Programs\@opencodedesktop\resources\opencode-cli.exe' --session ses_1`},
		{[]string{`C:\bin\codex.exe`, "Read it"}, "", `& C:\bin\codex.exe 'Read it'`},
		{[]string{"codex", "x"}, "", "codex x"},
	}
	for _, tc := range tests {
		if got := PowerShellCommand(tc.argv, tc.dir); got != tc.want {
			t.Errorf("PowerShellCommand(%q, %q) =\n %s\nwant\n %s", tc.argv, tc.dir, got, tc.want)
		}
	}
}

func TestFormatFor(t *testing.T) {
	cmd := provider.Command{Argv: []string{"claude", "--resume", "abc"}, Dir: `C:\proj`}
	onPath := func(string) (Executable, error) { return Executable{Path: `C:\bin\claude.exe`, OnPath: true}, nil }
	bundled := func(string) (Executable, error) { return Executable{Path: `C:\Users\me\.local\bin\claude.exe`}, nil }
	missing := func(name string) (Executable, error) { return Executable{}, ErrAgentNotFound }

	if got, want := formatFor("linux", provider.Command{Argv: cmd.Argv, Dir: "/p"}, bundled), "cd /p && claude --resume abc"; got != want {
		t.Errorf("linux = %q, want %q", got, want)
	}
	if got, want := formatFor("windows", cmd, onPath), `Set-Location -LiteralPath C:\proj -ErrorAction Stop; claude --resume abc`; got != want {
		t.Errorf("on PATH = %q, want %q", got, want)
	}
	if got, want := formatFor("windows", cmd, bundled), `Set-Location -LiteralPath C:\proj -ErrorAction Stop; & C:\Users\me\.local\bin\claude.exe --resume abc`; got != want {
		t.Errorf("bundled = %q, want %q", got, want)
	}
	if got, want := formatFor("windows", cmd, missing), `Set-Location -LiteralPath C:\proj -ErrorAction Stop; claude --resume abc`; got != want {
		t.Errorf("missing = %q, want %q", got, want)
	}
}

func TestValidateArgs(t *testing.T) {
	ok := [][]string{
		{"--resume", "abc"},
		{"Read C:\\Users\\me\\handoffs\\s-handoff.md completely, then wait."},
		{`C:\dir\`},
		{"100%"},
	}
	for _, args := range ok {
		if err := ValidateArgs(`C:\bin\codex.exe`, args); err != nil {
			t.Errorf("ValidateArgs(%q) = %v, want nil", args, err)
		}
	}
	bad := []struct {
		exe  string
		args []string
	}{
		{`C:\bin\codex.exe`, []string{""}},
		{`C:\bin\codex.exe`, []string{`say "hi"`}},
		{`C:\bin\codex.exe`, []string{"two\nlines"}},
		{`C:\bin\codex.exe`, []string{`C:\a dir\`}},
		{`C:\npm\codex.cmd`, []string{"100%"}},
		{`C:\npm\codex.CMD`, []string{"%PATH%"}},
	}
	for _, tc := range bad {
		if err := ValidateArgs(tc.exe, tc.args); !errors.Is(err, ErrUnsafeArgument) {
			t.Errorf("ValidateArgs(%q, %q) = %v, want ErrUnsafeArgument", tc.exe, tc.args, err)
		}
	}
}

func TestTerminalScript(t *testing.T) {
	dir := t.TempDir()
	find := func(name string) (Executable, error) {
		if name != "codex" {
			return Executable{}, ErrAgentNotFound
		}
		return Executable{Path: `C:\Codex\codex.exe`, OnPath: true}, nil
	}

	script, err := terminalScript(provider.Command{Argv: []string{"codex", "resume", "s1"}, Dir: dir}, find)
	if err != nil {
		t.Fatal(err)
	}
	// The console always runs the resolved path, even when PATH has it.
	if want := "& " + PowerShellQuote(`C:\Codex\codex.exe`) + " resume s1"; !strings.HasSuffix(script, want) || !strings.HasPrefix(script, "Set-Location -LiteralPath "+PowerShellQuote(dir)) {
		t.Errorf("script = %q", script)
	}

	if _, err := terminalScript(provider.Command{Argv: []string{"claude"}, Dir: dir}, find); !errors.Is(err, ErrAgentNotFound) {
		t.Errorf("missing agent: %v", err)
	}
	if _, err := terminalScript(provider.Command{Argv: []string{"codex"}, Dir: dir + "/gone"}, find); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing dir: %v", err)
	}
	if _, err := terminalScript(provider.Command{Argv: []string{"codex"}}, find); err == nil {
		t.Error("empty dir accepted")
	}
	if _, err := terminalScript(provider.Command{Argv: []string{"codex", `say "hi"`}, Dir: dir}, find); !errors.Is(err, ErrUnsafeArgument) {
		t.Errorf("unsafe arg: %v", err)
	}
}
