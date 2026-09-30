package remote

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func TestMain(m *testing.M) {
	if os.Getenv("AGENT_SESSIONS_SSH_FAKE") == "1" {
		os.Exit(sshFakeMain())
	}
	os.Exit(m.Run())
}

// fakeSSHPath is a copy of this test binary acting as ssh.
func fakeSSHPath(t *testing.T, mode string, extra map[string]string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ssh")
	// Copy, don't symlink. The test binary's path is reused across `go test`
	// runs and a symlink would exec a previous build.
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_SSH_FAKE", "1")
	t.Setenv("AGENT_SESSIONS_SSH_FAKE_MODE", mode)
	for k, v := range extra {
		t.Setenv(k, v)
	}
	return bin
}

func sshFakeMain() int {
	args := os.Args[1:]
	mode := os.Getenv("AGENT_SESSIONS_SSH_FAKE_MODE")
	if mode == "serve" {
		return fakeServe(args)
	}
	stdin, _ := readStdin()
	switch mode {
	case "probe-ok":
		return fakeProbe(args, true)
	case "probe-freebsd":
		return fakeProbe(args, false)
	case "deploy":
		return fakeDeploy(args, stdin, false)
	case "deploy-mismatch":
		return fakeDeploy(args, stdin, true)
	default:
		_, _ = os.Stderr.WriteString("unknown fake mode\n")
		return 2
	}
}

func readStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}

func scriptArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

func prefaceFromScript(script string) (string, bool) {
	// The remote command arrives as one POSIX-quoted argv. Probe and deploy
	// embed the preface literally; serve passes it as --nonce and the server
	// prints it. Accept either form.
	unwrapped := strings.ReplaceAll(script, `'\''`, "'")
	const prefix = "AGENT_SESSIONS_PREFACE_"
	idx := strings.Index(unwrapped, prefix)
	if idx < 0 {
		return "", false
	}
	rest := unwrapped[idx:]
	end := strings.IndexAny(rest, " \n'")
	if end < 0 {
		return rest, true
	}
	return rest[:end], true
}

// nonceFromServe reads the value of --nonce out of a quoted serve command.
func nonceFromServe(script string) (string, bool) {
	unwrapped := strings.ReplaceAll(script, `'\''`, "'")
	const flag = "--nonce "
	idx := strings.Index(unwrapped, flag)
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimLeft(unwrapped[idx+len(flag):], " '")
	end := strings.IndexAny(rest, " \n'")
	if end < 0 {
		return rest, rest != ""
	}
	return rest[:end], end > 0
}

func fakeProbe(args []string, linux bool) int {
	if !strings.Contains(strings.Join(args, "\x00"), "--") {
		_, _ = os.Stderr.WriteString("missing --\n")
		return 2
	}
	line, ok := prefaceFromScript(scriptArg(args))
	if !ok {
		_, _ = os.Stderr.WriteString("no preface\n")
		return 2
	}
	osName := "Linux"
	installed := "INSTALLED:/home/remote/.cache/agent-sessions/server/dev/agent-sessions-cli"
	if !linux {
		osName = "FreeBSD"
		installed = "NOT_INSTALLED"
	}
	_, _ = os.Stdout.WriteString("MOTD banner should be skipped\n" + line + "\n" + osName + "\nx86_64\n/home/remote\n" + installed + "\n")
	return 0
}

func fakeDeploy(args []string, stdin []byte, mismatch bool) int {
	script := scriptArg(args)
	switch {
	case strings.Contains(script, "sha256sum -c"):
		if !strings.Contains(script, "chmod 700") || !strings.Contains(script, "mv -f") {
			_, _ = os.Stderr.WriteString("unpack script incomplete\n")
			return 2
		}
		want, _ := strconv.Atoi(os.Getenv("AGENT_SESSIONS_SSH_FAKE_STDIN"))
		if want > 0 && len(stdin) != want {
			_, _ = os.Stderr.WriteString("stdin size mismatch\n")
			return 2
		}
		return 0
	case strings.Contains(script, " version"):
		line, ok := prefaceFromScript(script)
		if !ok {
			_, _ = os.Stderr.WriteString("no preface on verify\n")
			return 2
		}
		ver := os.Getenv("AGENT_SESSIONS_SSH_FAKE_VERSION")
		if mismatch {
			ver = "other-version"
		}
		_, _ = os.Stdout.WriteString(line + "\n" + ver + "\n")
		return 0
	default:
		return 0
	}
}

// fakeServe prints the preface the remote command asks for, answers initialize,
// then reads until stdin closes so the client can finish its calls.
func fakeServe(args []string) int {
	script := scriptArg(args)
	if !strings.Contains(script, "serve --stdio") {
		_, _ = os.Stderr.WriteString("not a serve command: " + script + "\n")
		return 2
	}
	nonce, ok := nonceFromServe(script)
	if !ok {
		_, _ = os.Stderr.WriteString("no nonce in: " + script + "\n")
		return 2
	}
	_, _ = os.Stdout.WriteString("login banner\nAGENT_SESSIONS_PREFACE_" + nonce + "\n")
	_, _ = os.Stderr.WriteString("server ready\n")

	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req rpc.Request
		if err := dec.Decode(&req); err != nil {
			return 0
		}
		if req.Method != rpc.MethodInitialize {
			_ = enc.Encode(rpc.Response{
				JSONRPC: rpc.JSONRPCVersion,
				ID:      req.ID,
				Error:   &rpc.ResponseError{Code: rpc.CodeMethodNotFound, Message: "not in fake"},
			})
			continue
		}
		raw, _ := json.Marshal(rpc.InitializeResult{
			ProtocolVersion: rpc.ProtocolVersion,
			AppVersion:      "dev",
			Capabilities:    rpc.Capabilities{Search: true, Export: true, Import: true},
		})
		_ = enc.Encode(rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: req.ID, Result: raw})
	}
}
