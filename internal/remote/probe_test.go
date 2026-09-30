package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/version"
)

func TestNormalizeOS(t *testing.T) {
	cases := map[string]string{
		"Linux":  "linux",
		"linux":  "linux",
		"Darwin": "darwin",
		"darwin": "darwin",
	}
	for in, want := range cases {
		got, err := NormalizeOS(in)
		if err != nil || got != want {
			t.Errorf("NormalizeOS(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeOS("FreeBSD"); !errors.Is(err, ErrUnsupportedOS) {
		t.Errorf("FreeBSD: got %v, want ErrUnsupportedOS", err)
	}
}

func TestNormalizeArch(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "amd64",
		"amd64":   "amd64",
		"aarch64": "arm64",
		"arm64":   "arm64",
	}
	for in, want := range cases {
		got, err := NormalizeArch(in)
		if err != nil || got != want {
			t.Errorf("NormalizeArch(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeArch("i386"); !errors.Is(err, ErrUnsupportedArch) {
		t.Errorf("i386: got %v, want ErrUnsupportedArch", err)
	}
}

func TestProbeHost(t *testing.T) {
	bin := fakeSSHPath(t, "probe-ok", nil)
	probe, err := ProbeHost(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, "dev")
	if err != nil {
		t.Fatalf("ProbeHost: %v", err)
	}
	if probe.OS != "linux" || probe.Arch != "amd64" || probe.Home != "/home/remote" {
		t.Fatalf("probe = %+v", probe)
	}
	if probe.InstalledVersion != "dev" || !strings.HasSuffix(probe.ServerPath, "/agent-sessions-cli") {
		t.Fatalf("installed = %q %q", probe.InstalledVersion, probe.ServerPath)
	}
}

func TestProbeHost_Unsupported(t *testing.T) {
	bin := fakeSSHPath(t, "probe-freebsd", nil)
	_, err := ProbeHost(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, "dev")
	if !errors.Is(err, ErrUnsupportedOS) {
		t.Fatalf("got %v, want ErrUnsupportedOS", err)
	}
}

func TestLocateServer(t *testing.T) {
	dir := t.TempDir()
	name := "agent-sessions-cli-linux-amd64.gz"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("gz"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR", dir)
	got, err := LocateServer("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("LocateServer = %q, want %q", got, path)
	}
	if _, err := LocateServer("plan9", "amd64"); err == nil {
		t.Fatal("expected missing bundle error")
	}
}

func TestDeployServer(t *testing.T) {
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "server.gz")
	payload := []byte("not-really-gzip")
	if err := os.WriteFile(gzPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	bin := fakeSSHPath(t, "deploy", map[string]string{
		"AGENT_SESSIONS_SSH_FAKE_STDIN":   "15",
		"AGENT_SESSIONS_SSH_FAKE_VERSION": version.Current(),
	})
	probe := &HostProbe{OS: "linux", Arch: "amd64", Home: "/home/remote"}
	got, err := DeployServer(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, probe, gzPath)
	if err != nil {
		t.Fatalf("DeployServer: %v", err)
	}
	if !strings.HasPrefix(got, "/home/remote/.cache/agent-sessions/server/") || !strings.HasSuffix(got, "/agent-sessions-cli") {
		t.Fatalf("installed path %q", got)
	}
	if !strings.Contains(got, version.Current()+"-") {
		t.Fatalf("path %q missing version tag", got)
	}
}

func TestDeployServer_VersionMismatch(t *testing.T) {
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "server.gz")
	if err := os.WriteFile(gzPath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := fakeSSHPath(t, "deploy-mismatch", map[string]string{
		"AGENT_SESSIONS_SSH_FAKE_STDIN": "1",
	})
	probe := &HostProbe{OS: "linux", Arch: "amd64", Home: "/home/remote"}
	_, err := DeployServer(context.Background(), "box", SSHOptions{Binary: bin, ControlMaster: "no"}, probe, gzPath)
	if err == nil || !strings.Contains(err.Error(), "other-version") {
		t.Fatalf("got %v, want version mismatch", err)
	}
}

func TestDeployServer_RejectsBadHome(t *testing.T) {
	_, err := DeployServer(context.Background(), "box", SSHOptions{}, &HostProbe{Home: "/tmp/evil\nhome"}, "")
	if err == nil {
		t.Fatal("expected invalid home error")
	}
}

func TestServerDirTag(t *testing.T) {
	got := ServerDirTag("0.3.0", "0123456789abcdef")
	if got != "0.3.0-01234567" {
		t.Fatalf("tag = %q", got)
	}
}
