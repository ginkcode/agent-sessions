package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/launch"
)

// WSLPrefix marks a host that is a WSL distribution on this Windows machine,
// reached through wsl.exe instead of ssh: "wsl:Ubuntu".
const WSLPrefix = "wsl:"

var (
	ErrWSLNotInstalled   = launch.ErrWSLNotInstalled
	ErrWSLDistroNotFound = errors.New("WSL distribution not found")
)

var distroRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// WSLDistro is a WSL distribution registered for the current user.
type WSLDistro struct {
	Name    string
	Default bool
}

// WSLTarget is the host name for distro.
func WSLTarget(distro string) string { return WSLPrefix + distro }

// ParseWSLTarget returns the distribution a WSL host names. ok is false for
// an SSH alias; the name is checked by ValidateDistro.
func ParseWSLTarget(host string) (distro string, ok bool) {
	return strings.CutPrefix(host, WSLPrefix)
}

// ValidateDistro accepts the names WSL itself allows, which can never be
// taken for a wsl.exe option.
func ValidateDistro(name string) error {
	if !distroRe.MatchString(name) {
		return fmt.Errorf("%w: WSL distribution %q", ErrInvalidHostAlias, name)
	}
	return nil
}

// ValidateHost checks an SSH alias, or the distribution a WSL host names.
func ValidateHost(host string) error {
	if distro, ok := ParseWSLTarget(host); ok {
		return ValidateDistro(distro)
	}
	return ValidateHostAlias(host)
}

// CheckWSLDistro returns ErrWSLNotInstalled or ErrWSLDistroNotFound when
// distro cannot be started.
func CheckWSLDistro(distro string) error {
	distros, err := ListWSLDistros()
	if err != nil {
		return err
	}
	for _, d := range distros {
		if strings.EqualFold(d.Name, distro) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrWSLDistroNotFound, distro)
}

// wslBinary is a variable so tests can run a fake wsl.exe.
var wslBinary = launch.WSLBinary

// wslLoginShell starts a WSL command's login shell. wsl.exe --exec runs no
// shell of its own, so SHELL may be unset.
const wslLoginShell = `"${SHELL:-/bin/sh}"`

// wslArgs returns the wsl.exe arguments that run remoteCmd in distro, as
// buildSSHArgs does for ssh.
func wslArgs(distro string, remoteCmd []string) ([]string, error) {
	if err := ValidateDistro(distro); err != nil {
		return nil, err
	}
	for _, arg := range remoteCmd {
		if strings.ContainsRune(arg, 0) {
			return nil, fmt.Errorf("%w: WSL command contains a NUL byte", launch.ErrUnsafeArgument)
		}
	}
	return launch.WSLExecArgs(distro, remoteLine(remoteCmd, wslLoginShell)), nil
}

// buildWSLCmd runs remoteCmd in distro with no window. WSL_UTF8 makes
// wsl.exe write its own errors in UTF-8 rather than UTF-16.
func buildWSLCmd(ctx context.Context, distro string, remoteCmd []string) (*exec.Cmd, error) {
	args, err := wslArgs(distro, remoteCmd)
	if err != nil {
		return nil, err
	}
	bin, err := wslBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	prepareSSHCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	return cmd, nil
}
