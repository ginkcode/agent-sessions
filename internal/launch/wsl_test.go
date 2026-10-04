package launch

import (
	"errors"
	"fmt"
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

func TestBootstrapLineGolden(t *testing.T) {
	got := bootstrapLine("cd -- 'a b'\nrun $X")
	want := `exec /bin/sh -c 'eval "$(printf "$1")"' sh 'cd -- \047a b\047\012run \044X'`
	if got != want {
		t.Errorf("bootstrapLine =\n%s\nwant\n%s", got, want)
	}
}

func TestOctalEscapeRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	s := "it's \"q\" $HOME `id` back\\slash\nline2\t100% !bang ünï \\0 \\n -dash"
	esc := OctalEscape(s)
	if strings.ContainsAny(esc, "'\"$`\n%!\t") || strings.Contains(esc, `\\`) {
		t.Fatalf("escape keeps special characters: %s", esc)
	}
	out, err := exec.Command("/bin/sh", "-c", `printf "$1"`, "sh", esc).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != s {
		t.Errorf("printf gave %q, want %q", out, s)
	}
}

func TestWSLExecArgs(t *testing.T) {
	got := WSLExecArgs("Ubuntu-24.04", "echo 'hi'")
	want := []string{"-d", "Ubuntu-24.04", "--cd", "~", "--exec", "/bin/sh", "-c", `eval "$(printf "$1")"`, "sh", `echo \047hi\047`}
	if !slices.Equal(got, want) {
		t.Errorf("WSLExecArgs = %q, want %q", got, want)
	}
}

// TestWSLExecArgsRunScript runs the arguments after --exec as WSL does.
func TestWSLExecArgsRunScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "out")
	agent := recordingAgent(t, out)
	script := posixScript(append([]string{agent}, posixTrickyArgs...), dir, fakeLoginShell(t))
	args := WSLExecArgs("Ubuntu", script)
	exe := slices.Index(args, "--exec")
	if b, err := exec.Command(args[exe+1], args[exe+2:]...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	checkRecorded(t, out, dir, filepath.Dir(agent), posixTrickyArgs)
}

func TestPosixTerminalScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	dir := posixTrickyDir(t)
	out := filepath.Join(platform.TempDir(t), "out")
	agent := recordingAgent(t, out)
	t.Setenv("PATH", filepath.Dir(agent)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", fakeLoginShell(t))
	t.Setenv("WSL_DISTRO_NAME", "")

	script, err := PosixTerminalScript(provider.Command{Argv: append([]string{"codex"}, posixTrickyArgs...), Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("/bin/sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	checkRecorded(t, out, dir, filepath.Dir(agent), posixTrickyArgs)

	if _, err := PosixTerminalScript(provider.Command{Argv: []string{"codex"}, Dir: filepath.Join(dir, "gone")}); err == nil {
		t.Error("script built for a missing directory")
	}
}

func TestFindSkipsWindowsCopyInWSL(t *testing.T) {
	platform.SkipWithoutModeBits(t)
	home := t.TempDir()
	env := map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}
	shim := "/mnt/c/Users/me/AppData/Roaming/npm/claude"
	f := finder{
		goos:     "linux",
		getenv:   func(k string) string { return env[k] },
		lookPath: func(string) (string, error) { return shim, nil },
		home:     home,
	}

	// Only the Windows copy: refused, naming it.
	if _, err := f.find("claude"); !errors.Is(err, ErrAgentNotFound) || !strings.Contains(err.Error(), shim+" is a Windows program") {
		t.Fatalf("Windows copy only: %v", err)
	}
	// A Linux install beats the Windows copy PATH found first.
	linux := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(linux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linux, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := f.find("claude"); err != nil || got.Path != linux || got.OnPath {
		t.Fatalf("Linux install: %+v, %v", got, err)
	}
	// Outside WSL, and for paths that are not a drive letter, PATH wins.
	for _, tc := range []struct{ distro, path string }{{"", shim}, {"Ubuntu", "/mnt/data/bin/claude"}, {"Ubuntu", "/usr/bin/claude"}} {
		env["WSL_DISTRO_NAME"] = tc.distro
		f.lookPath = func(string) (string, error) { return tc.path, nil }
		if got, err := f.find("claude"); err != nil || got.Path != tc.path || !got.OnPath {
			t.Errorf("distro %q, PATH %q: %+v, %v", tc.distro, tc.path, got, err)
		}
	}
}

// mountLine is a /proc/self/mountinfo line for point, escaped as the kernel
// escapes it.
func mountLine(id int, point, fstype, super string) string {
	esc := strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`).Replace(point)
	return fmt.Sprintf("%d 1 0:%d / %s rw,noatime shared:%d - %s src %s\n", id, id, esc, id, fstype, super)
}

const (
	wsl2Drive = "rw,aname=drvfs;path=C:\\;uid=1000;gid=1000;symlinkroot=/mnt/,cache=0x5,access=client,msize=65536,trans=fd,rfd=6,wfd=6"
	wslTools  = "ro,aname=drivers;fmask=222;dmask=222,cache=0x5,access=client,msize=65536,trans=fd,rfd=8,wfd=8"
)

func TestParseMountInfo(t *testing.T) {
	info := mountLine(1, "/", "ext4", "rw,discard") +
		mountLine(2, "/usr/lib/wsl/drivers", "9p", wslTools) +
		mountLine(3, "/mnt/c", "9p", wsl2Drive) +
		mountLine(4, "/mnt/c/linux", "ext4", "rw") +
		mountLine(5, "/mnt/d", "ext4", "rw") +
		mountLine(6, "/c", "9p", wsl2Drive) +
		mountLine(7, "/win drive\\x", "drvfs", "rw,case=off") +
		mountLine(8, "/mnt/e", "9p", wsl2Drive) +
		mountLine(9, "/mnt/e", "tmpfs", "rw") +
		"malformed line\n\n"
	mounts := parseMountInfo([]byte(info))
	if len(mounts) != 9 || mounts[6].point != `/win drive\x` || !mounts[6].windows || mounts[1].windows {
		t.Fatalf("mounts = %+v", mounts)
	}
	for file, want := range map[string]bool{
		"/mnt/c/Users/me/AppData/Roaming/npm/claude": true,  // default automount root
		"/c/Users/me/AppData/Roaming/npm/claude":     true,  // root = /
		`/win drive\x/npm/codex`:                     true,  // WSL 1, escaped point
		"/mnt/c":                                     true,  // the mount point itself
		"/mnt/d/tools/claude":                        false, // a Linux disk at /mnt/d
		"/mnt/c/linux/bin/claude":                    false, // Linux mount inside a drive
		"/mnt/cx/claude":                             false, // not below /mnt/c
		"/mnt/e/claude":                              false, // the later mount is on top
		"/usr/lib/wsl/drivers/x":                     false, // 9p, but not a drive
		"/usr/bin/claude":                            false,
	} {
		if got := onWindowsMount(mounts, file); got != want {
			t.Errorf("onWindowsMount(%q) = %v, want %v", file, got, want)
		}
	}
	if got := parseMountInfo([]byte("garbage\n")); len(got) != 0 {
		t.Errorf("garbage parsed as %+v", got)
	}
}

// TestFindWSLMountTable checks the mount table, never this machine's own,
// decides which copies are Windows ones, after resolving symlinks, for PATH
// hits and install directories alike.
func TestFindWSLMountTable(t *testing.T) {
	platform.SkipWithoutModeBits(t)
	// Mount points are compared with resolved paths, as on macOS /var.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	win := filepath.Join(root, "win drive")
	nested := filepath.Join(win, "linux")
	info := mountLine(1, "/", "ext4", "rw") +
		mountLine(2, "/mnt/c", "9p", wsl2Drive) +
		mountLine(3, "/mnt/c/linux", "ext4", "rw") +
		mountLine(4, "/mnt/d", "ext4", "rw") +
		mountLine(5, "/c", "9p", wsl2Drive) +
		mountLine(6, win, "9p", wsl2Drive) +
		mountLine(7, nested, "ext4", "rw")
	exe := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	env := map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}
	var pathHit string
	f := finder{
		goos:   "linux",
		getenv: func(k string) string { return env[k] },
		lookPath: func(string) (string, error) {
			if pathHit == "" {
				return "", errors.New("not found")
			}
			return pathHit, nil
		},
		home:      home,
		mountInfo: func() ([]byte, error) { return []byte(info), nil },
	}
	refused := func(name, want string) {
		t.Helper()
		if got, err := f.find(name); !errors.Is(err, ErrAgentNotFound) || !strings.Contains(err.Error(), want+" is a Windows program") {
			t.Errorf("%s: %+v, %v; want %s refused", name, got, err, want)
		}
	}
	found := func(name, want string, onPath bool) {
		t.Helper()
		if got, err := f.find(name); err != nil || got.Path != want || got.OnPath != onPath {
			t.Errorf("%s: %+v, %v; want %s", name, got, err, want)
		}
	}

	// PATH hits, by mount rather than by name.
	for hit, windows := range map[string]bool{
		"/mnt/c/agent-sessions-test/npm/claude":       true,
		"/c/agent-sessions-test/npm/claude":           true,
		"/mnt/d/agent-sessions-test/claude":           false,
		"/mnt/c/linux/agent-sessions-test/bin/claude": false,
		exe(filepath.Join(nested, "bin", "claude")):   false,
	} {
		pathHit = hit
		if windows {
			refused("claude", hit)
		} else {
			found("claude", hit, true)
		}
	}
	pathHit = ""

	// An install directory symlinked onto a drive is a Windows copy too, and
	// the search goes on past it.
	shim := exe(filepath.Join(win, "npm", "claude"))
	link := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shim, link); err != nil {
		t.Fatal(err)
	}
	refused("claude", link)
	pathHit = "/mnt/c/agent-sessions-test/npm/claude"
	refused("claude", pathHit) // the PATH hit is named first
	linux := exe(filepath.Join(home, ".npm-global", "bin", "claude"))
	found("claude", linux, false)
	pathHit = ""

	// So is a custom OpenCode directory on a drive.
	env["OPENCODE_INSTALL_DIR"] = filepath.Dir(exe(filepath.Join(win, "opencode", "opencode")))
	refused("opencode", filepath.Join(win, "opencode", "opencode"))
	ocLinux := exe(filepath.Join(home, ".opencode", "bin", "opencode"))
	found("opencode", ocLinux, false)

	// Without the mount table, only /mnt/<letter> counts as a drive.
	f.home = filepath.Join(root, "empty")
	f.mountInfo = func() ([]byte, error) { return nil, errors.New("no /proc") }
	pathHit = "/mnt/d/agent-sessions-test/claude"
	refused("claude", pathHit)
	pathHit = "/c/agent-sessions-test/npm/claude"
	found("claude", pathHit, true)

	// Outside WSL nothing is filtered or read.
	f.home = home
	env["WSL_DISTRO_NAME"] = ""
	f.mountInfo = func() ([]byte, error) { t.Error("mount table read outside WSL"); return nil, nil }
	pathHit = ""
	found("claude", link, false)
}
