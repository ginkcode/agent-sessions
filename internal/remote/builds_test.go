package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// installBuild creates a build directory under home, with an executable
// server binary unless bin is false.
func installBuild(t *testing.T, home, tag string, bin bool, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, filepath.FromSlash(serverRoot), tag)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if bin {
		if err := os.WriteFile(filepath.Join(dir, "agent-sessions-cli"), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProbeScript_ListsInstalledBuilds(t *testing.T) {
	platform.RequireCommand(t, "/bin/sh")
	home := filepath.Join(t.TempDir(), "home dir")
	now := time.Now()
	installBuild(t, home, "1.2.0-aaaaaaaa", true, now.Add(-time.Hour))
	installBuild(t, home, "1.2.0-bbbbbbbb", true, now)
	installBuild(t, home, "1.2.0-cccccccc", false, now)
	installBuild(t, home, "1.3.0-dddddddd", true, now)

	cmd := exec.Command("/bin/sh", "-c", probeScript("PREFACE", "1.2.0"))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] != "PREFACE" || lines[3] != home || lines[len(lines)-1] != "END" {
		t.Fatalf("probe output = %q", out)
	}
	want := []string{"INSTALLED:1.2.0-bbbbbbbb", "INSTALLED:1.2.0-aaaaaaaa"}
	if got := lines[4 : len(lines)-1]; !slices.Equal(got, want) {
		t.Errorf("installed lines = %q, want %q (newest first)", got, want)
	}
}

func TestPruneScript_RemovesOnlyUnusedBuilds(t *testing.T) {
	platform.RequireCommand(t, "/bin/sh")
	home := t.TempDir()
	old := time.Now().Add(-2 * PruneAfter)
	stale := installBuild(t, home, "1.0.0-aaaaaaaa", true, old)
	kept := installBuild(t, home, "1.0.0-bbbbbbbb", true, old)
	recent := installBuild(t, home, "0.9.0-cccccccc", true, time.Now())
	root := filepath.Dir(stale)

	if out, err := exec.Command("/bin/sh", "-c", pruneScript(root, "1.0.0-bbbbbbbb")).CombinedOutput(); err != nil {
		t.Fatalf("prune: %v: %s", err, out)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale build kept: %v", err)
	}
	for _, dir := range []string{kept, recent} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(dir), err)
		}
	}

	// A missing root is not an error.
	if err := exec.Command("/bin/sh", "-c", pruneScript(filepath.Join(home, "none"), "x")).Run(); err != nil {
		t.Errorf("prune of a missing root: %v", err)
	}
}

func TestChooseServer(t *testing.T) {
	dir := t.TempDir()
	gz := filepath.Join(dir, "agent-sessions-cli-linux-amd64.gz")
	if err := os.WriteFile(gz, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR", dir)
	sum, err := fileSHA256(gz)
	if err != nil {
		t.Fatal(err)
	}
	own := ServerDirTag(version.Current(), sum)
	other := version.Current() + "-ffffffff"

	probe := &HostProbe{OS: "linux", Arch: "amd64", Home: "/home/u", Installed: []string{other, own}}
	if bin, bundle := chooseServer(probe); bin != RemoteServerPath("/home/u", own) || bundle != "" {
		t.Errorf("own build installed: bin %q, bundle %q", bin, bundle)
	}

	// Another build of the same version is never run in place of ours.
	probe.Installed = []string{other}
	if bin, bundle := chooseServer(probe); bin != "" || bundle != gz {
		t.Errorf("only another build installed: bin %q, bundle %q", bin, bundle)
	}

	// Without a bundle for the host, the newest installed build is used.
	probe.OS = "plan9"
	if bin, bundle := chooseServer(probe); bin != RemoteServerPath("/home/u", other) || bundle != "" {
		t.Errorf("no bundle: bin %q, bundle %q", bin, bundle)
	}
	probe.Installed = nil
	if bin, bundle := chooseServer(probe); bin != "" || bundle != "" {
		t.Errorf("no bundle, nothing installed: bin %q, bundle %q", bin, bundle)
	}
}

func TestBuildDir(t *testing.T) {
	bin := RemoteServerPath("/home/u/", "1.0.0-aaaaaaaa")
	if dir, ok := buildDir(bin); !ok || dir != "/home/u/.cache/agent-sessions/server/1.0.0-aaaaaaaa" {
		t.Errorf("buildDir(%q) = %q, %v", bin, dir, ok)
	}
	for _, bin := range []string{"/usr/bin/agent-sessions-cli", "/home/u/.cache/agent-sessions/server/x/other"} {
		if dir, ok := buildDir(bin); ok {
			t.Errorf("buildDir(%q) = %q, want none", bin, dir)
		}
	}
}

func TestIsBuildTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"1.0.0-0123abcd":     true,
		"1.0.0-0123ABCD":     false,
		"1.0.0-0123abc":      false,
		"1.0.0-0123abcde":    false,
		"1.0.0-rc1-0123abcd": false,
		"1.0.1-0123abcd":     false,
		"1.0.0-0123abcg":     false,
	} {
		if got := isBuildTag("1.0.0", tag); got != want {
			t.Errorf("isBuildTag(%q) = %v, want %v", tag, got, want)
		}
	}
}
