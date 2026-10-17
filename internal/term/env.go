package term

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// loginEnvTimeout bounds the login shell LoginEnv runs.
const loginEnvTimeout = 10 * time.Second

// envMarker separates what the shell's startup files print from env's
// output.
const envMarker = "__AGENT_SESSIONS_ENV__"

var (
	loginEnvOnce sync.Once
	loginEnv     []string
)

// LoginEnv is the environment of the user's interactive login shell, such
// as the PATH a terminal app would have. An app started from the desktop
// often lacks what the shell's startup files add. It is read once; on
// Windows, or when the shell fails, it is the app's environment.
func LoginEnv() []string {
	loginEnvOnce.Do(func() {
		loginEnv = os.Environ()
		if runtime.GOOS == "windows" {
			return
		}
		if env := readLoginEnv(os.Getenv("SHELL"), loginEnvTimeout); len(env) > 0 {
			loginEnv = env
		}
	})
	return slices.Clone(loginEnv)
}

// readLoginEnv runs shell as an interactive login shell that prints its
// environment, or returns nil.
func readLoginEnv(shell string, timeout time.Duration) []string {
	if shell == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "echo "+envMarker+"; env -0")
	detachShell(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseEnv(out)
}

// parseEnv reads `env -0` output after envMarker's line.
func parseEnv(out []byte) []string {
	i := bytes.Index(out, []byte(envMarker+"\n"))
	if i < 0 {
		return nil
	}
	var env []string
	for _, entry := range strings.Split(string(out[i+len(envMarker)+1:]), "\x00") {
		if k, _, ok := strings.Cut(entry, "="); ok && k != "" {
			env = append(env, entry)
		}
	}
	return env
}
