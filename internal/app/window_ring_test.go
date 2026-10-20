package app

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Three windows share the claims dir; three hostClaims stand in for them.
func TestFocusNextWindowCyclesInOpenOrder(t *testing.T) {
	dir := t.TempDir()
	var raised [3]atomic.Int32
	windows := make([]*hostClaims, 3)
	for i := range windows {
		i := i
		windows[i] = newHostClaims(dir, func() { raised[i].Add(1) })
		t.Cleanup(windows[i].close)
		if err := windows[i].joinRing(); err != nil {
			t.Fatal(err)
		}
	}
	counts := func() [3]int32 {
		return [3]int32{raised[0].Load(), raised[1].Load(), raised[2].Load()}
	}

	if err := windows[0].focusNextWindow(true); err != nil {
		t.Fatal(err)
	}
	if got := counts(); got != [3]int32{0, 1, 0} {
		t.Fatalf("next from first raised %v", got)
	}
	// The last window wraps around to the first.
	if err := windows[2].focusNextWindow(true); err != nil {
		t.Fatal(err)
	}
	if got := counts(); got != [3]int32{1, 1, 0} {
		t.Fatalf("next from last raised %v", got)
	}
	// Backwards from the first is the last.
	if err := windows[0].focusNextWindow(false); err != nil {
		t.Fatal(err)
	}
	if got := counts(); got != [3]int32{1, 1, 1} {
		t.Fatalf("previous from first raised %v", got)
	}

	// A closed window drops out of the ring.
	windows[1].close()
	if err := windows[0].focusNextWindow(true); err != nil {
		t.Fatal(err)
	}
	if got := counts(); got != [3]int32{1, 1, 2} {
		t.Fatalf("next after closing the middle raised %v", got)
	}
}

func TestFocusNextWindowAlone(t *testing.T) {
	var raised atomic.Int32
	only := newHostClaims(t.TempDir(), func() { raised.Add(1) })
	t.Cleanup(only.close)
	if err := only.joinRing(); err != nil {
		t.Fatal(err)
	}
	if err := only.focusNextWindow(true); err != nil {
		t.Fatal(err)
	}
	if raised.Load() != 0 {
		t.Fatal("the only window raised itself")
	}
	// Not in the ring: an error, nothing raised.
	if err := newHostClaims(t.TempDir(), nil).focusNextWindow(true); err == nil {
		t.Fatal("cycling from outside the ring succeeded")
	}
}

// A window that exited without leaving (its lock released by the OS) is
// skipped and its entry removed; one that is gone without answering is
// passed over for the next.
func TestFocusNextWindowSkipsDeadWindows(t *testing.T) {
	dir := t.TempDir()
	var raised atomic.Int32
	self := newHostClaims(dir, nil)
	t.Cleanup(self.close)
	if err := self.joinRing(); err != nil {
		t.Fatal(err)
	}

	// Crashed: entry left behind, lock free.
	crashed := filepath.Join(dir, ringPrefix+"crashed")
	if err := os.WriteFile(crashed+".json", []byte(`{"port":1,"token":"x","joined":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stalled: holds its lock but its listener is gone.
	stalled := newHostClaims(dir, func() { t.Error("stalled window raised") })
	t.Cleanup(stalled.close)
	if err := stalled.joinRing(); err != nil {
		t.Fatal(err)
	}
	stalled.mu.Lock()
	_ = stalled.ln.Close()
	stalled.mu.Unlock()

	live := newHostClaims(dir, func() { raised.Add(1) })
	t.Cleanup(live.close)
	if err := live.joinRing(); err != nil {
		t.Fatal(err)
	}

	if err := self.focusNextWindow(true); err != nil {
		t.Fatal(err)
	}
	if raised.Load() != 1 {
		t.Fatalf("live window raised %d times, want 1", raised.Load())
	}
	if _, err := os.Stat(crashed + ".json"); !os.IsNotExist(err) {
		t.Fatalf("crashed window's entry kept: %v", err)
	}
}

func TestLeavingRingRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	h := newHostClaims(dir, nil)
	if err := h.joinRing(); err != nil {
		t.Fatal(err)
	}
	// Joining twice is fine.
	if err := h.joinRing(); err != nil {
		t.Fatal(err)
	}
	h.close()
	left, _ := filepath.Glob(filepath.Join(dir, ringPrefix+"*"))
	if len(left) != 0 {
		t.Fatalf("files left after closing: %v", left)
	}
}

func TestAppFocusNextWindowWithoutClaims(t *testing.T) {
	if err := (&App{}).FocusNextWindow(true); err != nil {
		t.Fatal(err)
	}
}
