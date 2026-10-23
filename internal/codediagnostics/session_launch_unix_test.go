//go:build darwin || linux

package codediagnostics

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostSessionLaunchFixedArgvAndProcessGroup(t *testing.T) {
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
		if processGone(child) {
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

// processGone reports whether pid no longer runs. A Linux zombie (already
// killed, awaiting reaping by its new parent) counts as gone: it executes
// nothing and holds no resources beyond its process-table slot.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(stat)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && len(s) > i+2 {
		return s[i+2] == 'Z'
	}
	return false
}
