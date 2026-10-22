//go:build darwin || linux || windows

package codediagnostics

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Only synthetic test-helper output; no installed checkers or source changes.
func TestCheckerHelper(t *testing.T) {
	mode := os.Getenv("CD_TEST_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "streams":
		for range 100 {
			_, _ = fmt.Fprintln(os.Stdout, "synthetic stdout")
			_, _ = fmt.Fprintln(os.Stderr, "synthetic stderr")
		}
		os.Exit(1)
	case "flood":
		for {
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("y"), 4096))
		}
	case "descendant", "descendantExit":
		child := exec.Command(os.Args[0], "-test.run=^TestCheckerHelper$")
		child.Env = []string{"CD_TEST_HELPER=child"}
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
		if mode == "descendantExit" {
			os.Exit(0)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "child":
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

func TestHostProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"streams", "flood", "descendant", "descendantExit"} {
		t.Run(mode, func(t *testing.T) {
			root, _, _ := fixture(t)
			tr := NewTrust(nil)
			c := Command{ProviderID: "helper", Executable: exe, Args: []string{"-test.run=^TestCheckerHelper$"}, Env: []string{"CD_TEST_HELPER=" + mode}, AdapterPolicy: "synthetic-helper-v1", AcceptedExits: []int{0, 1}}
			// Windows needs its system directory for process startup; still explicit.
			if runtime.GOOS == "windows" {
				c.Env = append(c.Env, "SystemRoot="+os.Getenv("SystemRoot"))
			}
			tools := []ToolInput{{ProviderID: c.ProviderID, Executable: exe, Policy: c.Policy()}}
			fp, err := Fingerprint(root, tools)
			if err != nil {
				t.Fatal(err)
			}
			approve(t, tr, root, tools, fp, "session")
			start := startProcess
			pid := make(chan int, 1)
			if mode == "descendant" || mode == "descendantExit" {
				start = func(in Invocation) (Process, error) {
					in.Stdout = &pidWriter{Writer: in.Stdout, pid: pid}
					return startProcess(in)
				}
			}
			s := NewSupervisor(tr, SupervisorOptions{Start: start, Timeout: 10 * time.Second, CleanupTimeout: 100 * time.Millisecond, CaptureBytes: 4096})
			defer s.Close()
			r := submit(t, s, root, tools, c, mode)
			childPID := 0
			if mode == "descendant" || mode == "descendantExit" {
				select {
				case childPID = <-pid:
				case <-time.After(10 * time.Second):
					t.Fatal("descendant not started")
				}
				if mode == "descendant" {
					r.Cancel()
				}
			}
			o := result(t, r)
			switch mode {
			case "streams":
				if o.Kind != OutcomeAccepted || !strings.Contains(string(o.Stdout), "synthetic stdout") || !strings.Contains(string(o.Stderr), "synthetic stderr") {
					t.Fatalf("stream outcome: %s", o.Kind)
				}
			case "flood":
				if o.Kind != OutcomeLimit || !o.Truncated || len(o.Stdout)+len(o.Stderr) > 4096 {
					t.Fatalf("flood outcome: %s", o.Kind)
				}
			case "descendantExit":
				// Parent exited but an inherited pipe remained open. WaitDelay
				// must unblock the wait and cleanup must still kill the child.
				if !o.Partial || o.Kind != OutcomeToolFailed {
					t.Fatalf("blocked pipe outcome: %s", o.Kind)
				}
				assertDescendantStopped(t, childPID)
			case "descendant":
				if o.Kind != OutcomeCanceled {
					t.Fatalf("cancel outcome: %s", o.Kind)
				}
				assertDescendantStopped(t, childPID)
			}
			// Supervisor itself never writes source, config or captures to disk.
			data, err := os.ReadFile(filepath.Join(root.Project().Root, "source.go"))
			if err != nil || string(data) != "synthetic source.go" {
				t.Fatal("source changed")
			}
		})
	}
	t.Run("launchFailure", func(t *testing.T) {
		_, err := startProcess(Invocation{Executable: filepath.Join(t.TempDir(), "missing"), CleanupTimeout: time.Millisecond})
		if err == nil {
			t.Fatal("missing executable launched")
		}
	})
}

type pidWriter struct {
	ioBuffer []byte
	Writer   interface{ Write([]byte) (int, error) }
	pid      chan int
	sent     bool
}

func (p *pidWriter) Write(b []byte) (int, error) {
	if !p.sent {
		p.ioBuffer = append(p.ioBuffer, b...)
		if i := bytes.IndexByte(p.ioBuffer, '\n'); i >= 0 {
			n, _ := strconv.Atoi(string(p.ioBuffer[:i]))
			p.pid <- n
			p.sent = true
		}
	}
	return p.Writer.Write(b)
}

func TestSupervisorDeniedLaunch(t *testing.T) {
	root, tools, c := approvedCommand(t, NewTrust(nil))
	s := NewSupervisor(NewTrust(nil), SupervisorOptions{Start: func(Invocation) (Process, error) {
		t.Error("unapproved process started")
		return nil, fmt.Errorf("denied")
	}})
	defer s.Close()
	_, err := s.Submit(context.Background(), root, tools, c, "denied", false)
	code(t, err, "approvalRequired")
}
