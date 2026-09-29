package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeHome points HOME at a temp dir and PATH at the system dirs only, like
// an app started from a desktop launcher that never read ~/.bashrc.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("OPENCODE_INSTALL_DIR", "")
	return home
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCLIFindsInstallDirOutsidePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	home := fakeHome(t)
	if _, err := resolveCLI("opencode"); !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("want ErrCLINotFound before install, got %v", err)
	}

	bin := filepath.Join(home, ".opencode", "bin", "opencode")
	writeScript(t, bin, "exit 0")
	got, err := resolveCLI("opencode")
	if err != nil || got != bin {
		t.Fatalf("resolveCLI = %q, %v; want %q", got, err, bin)
	}

	// A non-executable file is not a CLI.
	codex := filepath.Join(home, ".local", "bin", "codex")
	writeScript(t, codex, "exit 0")
	if err := os.Chmod(codex, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCLI("codex"); !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("want ErrCLINotFound for non-executable codex, got %v", err)
	}
}

func TestRunStandaloneReportsCLIFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	home := fakeHome(t)
	ctx := context.Background()

	err := runStandalone(ctx, []string{"opencode", "session", "delete"}, home, nil)
	if !errors.Is(err, ErrCLINotFound) || !strings.Contains(err.Error(), "opencode CLI not found") {
		t.Fatalf("missing CLI: got %v", err)
	}

	// The CLI's directory leads PATH, so an npm shim finds its node.
	bin := filepath.Join(home, ".opencode", "bin")
	writeScript(t, filepath.Join(bin, "opencode"), `case "$PATH" in "`+bin+`":*) ;; *) echo "bad PATH $PATH" >&2; exit 9;; esac
echo "loading" >&2
echo "Error: cannot open `+home+`/.local/share/opencode/opencode.db with OPENAI_API_KEY=sk-`+`proj-1234567890abcdefghijklmnopqrstuvwxyz" >&2
exit 3`)
	err = runStandalone(ctx, []string{"opencode", "session", "delete"}, home, nil)
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	for _, want := range []string{"opencode exited with status 3", "Error: cannot open ~/.local/share/opencode/opencode.db", "[REDACTED"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	for _, leak := range []string{home, "proj-1234567890", "loading"} {
		if strings.Contains(msg, leak) {
			t.Errorf("message %q leaks %q", msg, leak)
		}
	}

	writeScript(t, filepath.Join(bin, "opencode"), "exit 0")
	if err := runStandalone(ctx, []string{"opencode", "session", "delete"}, home, nil); err != nil {
		t.Fatalf("successful run: %v", err)
	}
}

func TestCLIMessageBoundsOutput(t *testing.T) {
	long := strings.Repeat("x", 1000)
	got := cliMessage([]byte("first\n\n" + long + "\x1b[0m\n\n"))
	if len([]rune(got)) != 241 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d, %q", len([]rune(got)), got[len(got)-8:])
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Fatal("control characters kept")
	}
	if cliMessage([]byte("\n  \n")) != "" {
		t.Fatal("blank stderr should yield no detail")
	}
}
