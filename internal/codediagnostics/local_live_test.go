package codediagnostics

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDiscoverGoLiveBindsToolchainAndHoldsOtherLanguages(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"go.mod": "module example.test/a\n\ngo 1.26\n", "a.go": "package a", "b.ts": "", "c.svelte": ""})
	bin, env := goplsHost(t, "v0.24.0", goplsModulePath)
	if err := os.WriteFile(filepath.Join(env.GoRoot, "VERSION"), []byte("go1.26.8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	regs, err := DiscoverGoLive(root, []string{bin}, env)
	if err != nil || len(regs) != 3 {
		t.Fatal(err, regs)
	}
	g := regs[0]
	if g.ProviderID != goplsProviderID || g.Availability != "available" || g.Live == nil {
		t.Fatalf("%+v", g)
	}
	tools := g.Live.Server.Tools()
	ids := []string{}
	for _, tool := range tools {
		ids = append(ids, tool.ProviderID)
	}
	if !slices.Equal(ids, []string{goplsProviderID, "gopls-toolchain-bin-go", "gopls-toolchain-VERSION"}) {
		t.Fatal("toolchain not bound", ids)
	}
	for _, r := range regs[1:] {
		if r.Availability != "unsupported" || r.Live == nil || r.Live.Resolve != nil || r.Live.State.Coverage.Kind != "openedDocuments" {
			t.Fatalf("%+v", r)
		}
	}
	docs, err := DiscoveryDocuments(root, regs, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := docs.Resolve(root, "a.go")
	if !ok || server.LanguageID != "go" || len(server.Config.ApprovalTools) != 3 {
		t.Fatalf("%v %+v", ok, server)
	}
	for _, p := range []string{"b.ts", "c.svelte"} {
		if _, ok := docs.Resolve(root, p); ok {
			t.Fatal("unsupported language offered", p)
		}
	}
	// Changing toolchain metadata changes the composite fingerprint.
	before, err := Fingerprint(root, tools)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.GoRoot, "VERSION"), []byte("go1.99.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(root, tools)
	if err != nil || after == before {
		t.Fatal("toolchain change kept the fingerprint", err)
	}
	// No toolchain at all is an independent status, never an error.
	env.GoRoot = ""
	regs, err = DiscoverGoLive(root, []string{bin}, env)
	if err != nil || regs[0].Availability != "missingTool" {
		t.Fatal(err, regs[0].Availability)
	}
	if _, err := DiscoverGoLive(root, make([]string, 65), env); errCode(err) != "limitExceeded" {
		t.Fatal(err)
	}
}

func TestHostSessionLaunchFixedArgvAndProcessGroup(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix launcher")
	}
	if _, _, err := HostSessionLaunch(Invocation{Executable: "sh"}); errCode(err) != "invalidArgument" {
		t.Fatal("relative executable accepted", err)
	}
	dir := t.TempDir()
	// The child echoes its argv/env, starts a grandchild and reports its pid.
	p, streams, err := HostSessionLaunch(Invocation{
		Executable: "/bin/sh", Args: []string{"-c", `echo "$0 $MARK $(pwd -P)"; sleep 60 & echo $!; cat`, "fixed"},
		Directory: dir, Env: []string{"MARK=env-only", "PATH=/usr/bin:/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	out := bufio.NewReader(streams.Stdout)
	line, err := out.ReadString('\n')
	real, _ := filepath.EvalSymlinks(dir)
	if err != nil || strings.TrimSpace(line) != "fixed env-only "+real {
		t.Fatalf("%q %v", line, err)
	}
	line, err = out.ReadString('\n')
	child, convErr := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || convErr != nil {
		t.Fatal(line, err, convErr)
	}
	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(child, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grandchild survived process-group termination")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = streams.Stdin.Close()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(streams.Stderr); err == nil {
		t.Fatal("stderr read end still open after Close")
	}
	if err := p.Terminate(); err != nil {
		t.Fatal("terminate after exit", err)
	}
}
