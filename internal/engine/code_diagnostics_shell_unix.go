//go:build darwin || linux

package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const loginPathMarker = "__AGENT_SESSIONS_LOGIN_PATH__"

var loginPath struct {
	once sync.Once
	dirs []string
}

// loginShellPath returns the PATH of the user's login shell, read once per
// process, as editors do: an app started from the Dock or Finder, or a server
// started through ssh's non-interactive shell, does not see what version
// managers and shell startup files add there. It runs the user's own shell
// (`$SHELL -l -i -c`) in the home directory, never in a project, with a 5 s
// limit and its process group killed afterwards. Empty on failure.
var loginShellPath = func(home string) []string {
	loginPath.once.Do(func() { loginPath.dirs = readLoginShellPath(home) })
	return loginPath.dirs
}

func readLoginShellPath(home string) []string {
	shell := userShell()
	if shell == "" || !filepath.IsAbs(home) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// fish joins a quoted PATH with colons, like POSIX shells.
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf '%s%s%s' `+loginPathMarker+` "$PATH" `+loginPathMarker)
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	cmd.Stdout = &out
	_ = cmd.Run()
	if cmd.Process != nil {
		// Startup files may leave background jobs behind.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_, rest, ok := bytes.Cut(out.buf.Bytes(), []byte(loginPathMarker))
	if !ok {
		return nil
	}
	value, _, ok := bytes.Cut(rest, []byte(loginPathMarker))
	if !ok {
		return nil
	}
	var dirs []string
	for _, d := range filepath.SplitList(string(value)) {
		if filepath.IsAbs(d) && filepath.Clean(d) == d && len(dirs) < 64 {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// userShell is $SHELL, else the account's shell from /etc/passwd, else the
// platform default.
func userShell() string {
	if s := os.Getenv("SHELL"); filepath.IsAbs(s) {
		return s
	}
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer func() { _ = f.Close() }()
		uid := strconv.Itoa(os.Getuid())
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Split(sc.Text(), ":")
			if len(fields) == 7 && fields[2] == uid && filepath.IsAbs(fields[6]) {
				return fields[6]
			}
		}
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

type limitedBuffer struct{ buf bytes.Buffer }

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if room := 256<<10 - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
