package launch

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestPanelPowerShellPromptCwd(t *testing.T) {
	ps, err := exec.LookPath("pwsh")
	if err != nil {
		ps, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell not installed")
	}
	dir := filepath.Join(t.TempDir(), "spaces é日本 %#")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script := "function global:prompt { 'EXISTING_PROMPT' }; " + panelPowerShellHook + "\nSet-Location -LiteralPath '" + strings.ReplaceAll(dir, "'", "''") + "'\nprompt\nSet-Location -LiteralPath '" + strings.ReplaceAll(filepath.Join(dir, "missing"), "'", "''") + "' -ErrorAction SilentlyContinue\nprompt\n"
	b, err := exec.Command(ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	matches := regexp.MustCompile("\x1b\\]7;([^\a]*)\a").FindAllSubmatch(b, -1)
	if len(matches) != 2 {
		t.Fatalf("OSC7 output %q", b)
	}
	want, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		u, err := url.Parse(string(match[1]))
		if err != nil {
			t.Fatal(err)
		}
		got := u.Path
		if runtime.GOOS == "windows" {
			got = strings.TrimPrefix(got, "/") // file:///C:/... has a path of /C:/...
		}
		// Compare folders, not spellings: TEMP may use 8.3 short names
		// (RUNNER~1) where PowerShell reports the long ones.
		fi, err := os.Stat(filepath.FromSlash(got))
		if err != nil || !os.SameFile(fi, want) {
			t.Fatalf("path %q, want %q (%v)", u.Path, dir, err)
		}
	}
	if strings.Count(string(b), "EXISTING_PROMPT") != 2 {
		t.Fatalf("original prompt lost: %q", b)
	}
}
