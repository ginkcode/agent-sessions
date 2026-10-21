package launch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRemotePanelAgentStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells")
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, mode := range []string{"rc", "profile", "profile-sources-rc"} {
			t.Run(shell+"/"+mode, func(t *testing.T) {
				path, err := exec.LookPath(shell)
				if err != nil {
					t.Skip(err)
				}
				home := t.TempDir()
				dir := filepath.Join(home, "project ' space")
				tmp := filepath.Join(home, "tmp")
				for _, d := range []string{dir, tmp} {
					if err := os.Mkdir(d, 0700); err != nil {
						t.Fatal(err)
					}
				}
				write := func(name, text string) {
					t.Helper()
					p := filepath.Join(home, name)
					if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(text), 0600); err != nil {
						t.Fatal(err)
					}
				}
				// A prompt-time hook simulates mise selecting tools for this project.
				switch shell {
				case "bash":
					activation := "export ACTIVATED=${ACTIVATED}x\nPROMPT_COMMAND='export PROJECT_READY=$PWD'\n"
					if mode == "profile" {
						write(".profile", activation)
					} else {
						write(".bashrc", activation)
						if mode == "profile-sources-rc" {
							write(".bash_profile", ". \"$HOME/.bashrc\"\n")
						}
					}
				case "zsh":
					activation := "export ACTIVATED=${ACTIVATED}x\nprecmd() { export PROJECT_READY=$PWD; }\n"
					if mode == "profile" {
						write(".zprofile", activation)
					} else {
						write(".zshrc", activation)
					}
				case "fish":
					write(".config/fish/config.fish", "set -gx ACTIVATED x\nfunction project_ready --on-event fish_prompt\n set -gx PROJECT_READY $PWD\nend\n")
				}
				agent := filepath.Join(home, "agent")
				out := filepath.Join(home, "result")
				if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf '%s\\n' \"$ACTIVATED\" \"$PROJECT_READY\" \"$PWD\" \"$1\" > "+ShellEscape(out)+"\nexit 7\n"), 0700); err != nil {
					t.Fatal(err)
				}
				script := posixPanelAgentScript([]string{agent, "argument ' $ quoted"}, dir)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				// Exercise the actual shell-neutral SSH transport, with stdin left for
				// the agent rather than injecting a command into its terminal input.
				cmd := exec.CommandContext(ctx, path, "-c", JoinCommand(PosixBootstrapArgv(script)))
				cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "TMPDIR="+tmp, "TERM=dumb", "SHELL="+path, "ACTIVATED=", "PROJECT_READY=")
				b, err := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 7 {
					t.Fatalf("want exit 7: %v: %s", err, b)
				}
				got, err := os.ReadFile(out)
				if err != nil {
					t.Fatalf("%v: %s", err, b)
				}
				want := "x\n" + dir + "\n" + dir + "\nargument ' $ quoted\n"
				if string(got) != want {
					t.Fatalf("got %q, want %q; output %s", got, want, b)
				}
				files, err := os.ReadDir(tmp)
				if err != nil || len(files) != 0 {
					t.Fatalf("temporary files not cleaned: %v %v", files, err)
				}
				if !strings.Contains(string(b), "exited with status 7") {
					t.Fatalf("missing status: %s", b)
				}
			})
		}
	}
}
