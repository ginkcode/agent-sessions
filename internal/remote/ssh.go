package remote

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SSHOptions configures ssh invocation parameters.
type SSHOptions struct {
	Binary              string
	ConfigFile          string
	ControlPath         string
	ControlMaster       string // "auto", "yes", "no"
	ControlPersist      string // e.g. "10m"
	ServerAliveInterval int
	ServerAliveCountMax int
	NoTTY               bool
	ForceTTY            bool
	AskpassBinary       string
	AskpassSock         string
	AskpassToken        string
	ExtraOptions        []string
}

// LoginShell, as the first element of a remote command, runs the rest under
// the remote user's own shell. It is sent as "$SHELL" so the remote side
// expands it; every other element is single-quoted.
const LoginShell = "$SHELL"

// maxControlDirLen keeps ControlPath sockets bindable. sun_path holds 104
// bytes on macOS (108 on Linux) including the NUL, and the ssh master first
// binds "<ControlPath>.<16 random chars>" with ControlPath "<dir>/cm-%C"
// (%C is 40 hex chars).
const maxControlDirLen = 104 - 1 - len("/cm-") - 40 - len(".0123456789abcdef")

// ControlDir returns a private 0700 directory for ControlMaster sockets:
// $XDG_RUNTIME_DIR/agent-sessions, else as-ssh-<uid> in the temp dir, else
// /tmp/as-ssh-<uid> when that path is too long for a socket (macOS $TMPDIR).
// An existing directory must be ours, private and not a symlink: another
// local user who pre-creates it could otherwise plant a fake mux socket.
func ControlDir() (string, error) {
	var base string
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		base = filepath.Join(runtimeDir, "agent-sessions")
	}
	if base == "" || len(base) > maxControlDirLen {
		base = filepath.Join(os.TempDir(), fmt.Sprintf("as-ssh-%d", os.Getuid()))
	}
	if len(base) > maxControlDirLen {
		base = fmt.Sprintf("/tmp/as-ssh-%d", os.Getuid())
	}

	if err := os.MkdirAll(base, 0700); err != nil {
		return "", fmt.Errorf("create ssh control dir: %w", err)
	}
	if err := checkPrivateDir(base); err != nil {
		return "", fmt.Errorf("ssh control dir %s: %w", base, err)
	}
	return base, nil
}

// DefaultControlPath returns the standard %C socket path in the control directory.
func DefaultControlPath() (string, error) {
	dir, err := ControlDir()
	if err != nil {
		return "", err
	}
	// OpenSSH replaces %C with a 40-character SHA1 hash of (%l%h%p%r).
	return filepath.Join(dir, "cm-%C"), nil
}

// BuildSSHArgs builds safe, argv-only arguments for the ssh client.
// It enforces flag validation, immune to shell expansion and flag injection.
func BuildSSHArgs(alias string, remoteCmd []string, opts SSHOptions) ([]string, error) {
	if err := ValidateHostAlias(alias); err != nil {
		return nil, err
	}

	interval := opts.ServerAliveInterval
	if interval <= 0 {
		interval = 15
	}
	countMax := opts.ServerAliveCountMax
	if countMax <= 0 {
		countMax = 3
	}

	var args []string

	if opts.ConfigFile != "" {
		args = append(args, "-F", opts.ConfigFile)
	}

	args = append(args,
		"-o", "BatchMode=no",
		"-o", "ExitOnForwardFailure=yes",
		"-o", fmt.Sprintf("ServerAliveInterval=%d", interval),
		"-o", fmt.Sprintf("ServerAliveCountMax=%d", countMax),
	)

	cm := opts.ControlMaster
	if cm == "" {
		cm = "auto"
	}
	if cm != "no" {
		cPath := opts.ControlPath
		if cPath == "" {
			var err error
			cPath, err = DefaultControlPath()
			if err != nil {
				return nil, err
			}
		}
		persist := opts.ControlPersist
		if persist == "" {
			persist = "10m"
		}
		args = append(args,
			"-o", "ControlMaster="+cm,
			"-o", "ControlPersist="+persist,
			"-o", "ControlPath="+cPath,
		)
	}

	if opts.NoTTY {
		args = append(args, "-T")
	} else if opts.ForceTTY {
		args = append(args, "-t")
	}

	for _, opt := range opts.ExtraOptions {
		if opt != "" {
			args = append(args, "-o", opt)
		}
	}

	// Delimiter before alias prevents flag injection
	args = append(args, "--", alias)

	if len(remoteCmd) > 0 {
		if remoteCmd[0] == LoginShell {
			line := `"$SHELL"`
			if rest := remoteCmd[1:]; len(rest) > 0 {
				line += " " + QuoteArgs(rest)
			}
			args = append(args, line)
		} else {
			args = append(args, QuoteArgs(remoteCmd))
		}
	}

	return args, nil
}

// BuildSSHCmd constructs an exec.Cmd configured with SSH options and environment.
func BuildSSHCmd(ctx context.Context, alias string, remoteCmd []string, opts SSHOptions) (*exec.Cmd, error) {
	args, err := BuildSSHArgs(alias, remoteCmd, opts)
	if err != nil {
		return nil, err
	}

	bin := opts.Binary
	if bin == "" {
		bin = "ssh"
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()

	// Configure askpass environment
	if opts.AskpassBinary != "" {
		cmd.Env = append(cmd.Env,
			"SSH_ASKPASS="+opts.AskpassBinary,
			"SSH_ASKPASS_REQUIRE=force",
		)
	}
	if opts.AskpassSock != "" {
		cmd.Env = append(cmd.Env, "AGENT_SESSIONS_ASKPASS_SOCK="+opts.AskpassSock)
	}
	if opts.AskpassToken != "" {
		cmd.Env = append(cmd.Env, "AGENT_SESSIONS_ASKPASS_TOKEN="+opts.AskpassToken)
	}
	if os.Getenv("DISPLAY") == "" {
		// A non-empty DISPLAY is required by OpenSSH on some UNIX systems to invoke SSH_ASKPASS
		cmd.Env = append(cmd.Env, "DISPLAY=dummy:0")
	}

	return cmd, nil
}

// StopControlMaster gracefully shuts down the multiplexed connection for an alias.
func StopControlMaster(ctx context.Context, sshBin string, alias string, opts SSHOptions) error {
	if err := ValidateHostAlias(alias); err != nil {
		return err
	}
	if sshBin == "" {
		sshBin = "ssh"
	}

	cPath := opts.ControlPath
	if cPath == "" {
		var err error
		cPath, err = DefaultControlPath()
		if err != nil {
			return err
		}
	}

	var args []string
	if opts.ConfigFile != "" {
		args = append(args, "-F", opts.ConfigFile)
	}
	args = append(args, "-O", "exit", "-o", "ControlPath="+cPath, "--", alias)

	cmd := exec.CommandContext(ctx, sshBin, args...)
	return cmd.Run()
}
