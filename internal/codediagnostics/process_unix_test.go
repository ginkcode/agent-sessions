//go:build darwin || linux

package codediagnostics

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func assertDescendantStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		if runtime.GOOS == "linux" {
			// Container PID 1 may leave adopted zombies unreaped; they cannot execute.
			data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if strings.Contains(string(data), ") Z ") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d remains alive", pid)
}
