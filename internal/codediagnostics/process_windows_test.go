package codediagnostics

import (
	"testing"

	"golang.org/x/sys/windows"
)

func assertDescendantStopped(t *testing.T, pid int) {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	} // the process object may already be gone
	defer func() { _ = windows.CloseHandle(h) }()
	status, err := windows.WaitForSingleObject(h, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant still running: %v %v", status, err)
	}
}
