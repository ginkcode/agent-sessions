package launch

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPanelCwdEncoder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX encoder")
	}
	for _, path := range []string{"/tmp/a b/é日本", "/tmp/%?#\n\x1b\\"} {
		b, err := exec.Command("/bin/sh", "-c", panelCwdEncoder, "sh", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		uri := strings.TrimSuffix(strings.TrimPrefix(string(b), "\x1b]7;"), "\a")
		parsed, err := url.Parse(uri)
		if err != nil || parsed.Path != path || parsed.Host != "localhost" {
			t.Fatalf("%q: %v, %v", b, parsed, err)
		}
	}
}

func TestPanelShellPromptCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells")
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, array := range []bool{false, true} {
			t.Run(shell+map[bool]string{false: "/scalar", true: "/array"}[array], func(t *testing.T) {
				path, err := exec.LookPath(shell)
				if err != nil {
					t.Skip(err)
				}
				home, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(home, "spaces é日本 %#")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				hooks := "PROMPT_COMMAND='printf EXISTING_HOOK'\n"
				if array {
					hooks = "PROMPT_COMMAND=('printf FIRST_HOOK' 'printf EXISTING_HOOK')\n"
				}
				profile := ".bash_profile"
				if shell == "zsh" {
					profile = ".zshrc"
					hooks = "precmd() { printf EXISTING_HOOK; }\nprecmd_functions=(existing_hook)\nexisting_hook() { printf FIRST_HOOK; }\n"
				}
				if shell == "fish" {
					profile = ".config/fish/config.fish"
					hooks = "function fish_prompt; printf EXISTING_HOOK; end\n"
					if array {
						hooks += "function existing_hook --on-event fish_prompt; printf FIRST_HOOK; end\n"
					}
					if err := os.MkdirAll(filepath.Dir(filepath.Join(home, profile)), 0700); err != nil {
						t.Fatal(err)
					}
				}
				original := []byte(hooks)
				if shell != "fish" {
					original = []byte("PS1='test> '\n" + hooks)
				}
				if err := os.WriteFile(filepath.Join(home, profile), original, 0600); err != nil {
					t.Fatal(err)
				}
				tmp := filepath.Join(home, "tmp")
				if err := os.Mkdir(tmp, 0700); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				script := panelPosixShellScript(ShellEscape(path))
				if array {
					// The same transport used by SSH, and /bin/sh bootstrap used
					// by WSL, must retain both quoting and prompt integration.
					script = JoinCommand(PosixBootstrapArgv(PanelShellScript(home)))
				}
				cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
				cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "TMPDIR="+tmp, "TERM=dumb", "SHELL="+path)
				cmd.Dir = home
				cmd.Stdin = strings.NewReader("cd -- " + ShellEscape(dir) + "\ncd -- " + ShellEscape(filepath.Join(home, "missing")) + "\nexit 0\n")
				b, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %s", err, b)
				}
				matches := regexp.MustCompile("\x1b\\]7;([^\a]*)\a").FindAllSubmatch(b, -1)
				var got []string
				for _, match := range matches {
					u, err := url.Parse(string(match[1]))
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, u.Path)
				}
				if want := []string{home, dir, dir}; !reflect.DeepEqual(got, want) {
					t.Fatalf("cwd = %q, want %q; output %q", got, want, b)
				}
				if strings.Count(string(b), "EXISTING_HOOK") != 3 {
					t.Fatalf("existing prompt hook lost: %q", b)
				}
				if array && strings.Count(string(b), "FIRST_HOOK") != 3 {
					t.Fatalf("array prompt hook lost: %q", b)
				}
				after, _ := os.ReadFile(filepath.Join(home, profile))
				if string(after) != string(original) {
					t.Fatal("profile modified")
				}
				files, err := os.ReadDir(tmp)
				if err != nil || len(files) != 0 {
					t.Fatalf("wrappers not cleaned: %v %v", files, err)
				}
			})
		}
	}
}

func TestPanelShellBootstrapConstruction(t *testing.T) {
	script := PanelShellScript("/space and 'quote/é")
	if !strings.Contains(script, "cd -- "+ShellEscape("/space and 'quote/é")) || !strings.Contains(script, `__as_shell="${SHELL:-/bin/sh}"`) {
		t.Fatal(script)
	}
	for _, hook := range []string{"PROMPT_COMMAND=(", "precmd_functions+=", "--on-event fish_prompt", "mktemp -d", "trap 'rm -rf"} {
		if !strings.Contains(script, hook) {
			t.Errorf("missing %q", hook)
		}
	}
	for _, part := range []string{"$global:__AgentSessionsPrompt = $function:prompt", "& $global:__AgentSessionsPrompt", "[Uri]::EscapeDataString", "$PWD.ProviderPath", "FileSystem"} {
		if !strings.Contains(panelPowerShellHook, part) {
			t.Errorf("PowerShell hook missing %q", part)
		}
	}
}
