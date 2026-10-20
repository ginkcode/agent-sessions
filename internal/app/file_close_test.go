package app

import (
	"sync"
	"testing"
)

func TestFileCloseGuard(t *testing.T) {
	var a App
	if prevent, id := a.fileClose.request(); prevent || id != 0 {
		t.Fatal("clean startup should close")
	}
	if !a.EnableFileEditing() {
		t.Fatal("cannot arm guard")
	}
	prevent, id := a.fileClose.request()
	if !prevent || id == 0 {
		t.Fatal("armed window must defer close")
	}
	if a.EnableFileEditing() {
		t.Fatal("editing cannot begin during close")
	}
	if prevent, duplicate := a.fileClose.request(); !prevent || duplicate != 0 {
		t.Fatal("duplicate request")
	}
	if a.fileClose.complete(id+1, true) {
		t.Fatal("stale approval")
	}
	if a.fileClose.complete(id, false) {
		t.Fatal("cancel must not authorize")
	}
	if !a.EnableFileEditing() {
		t.Fatal("cancel should unblock editing")
	}
	prevent, next := a.fileClose.request()
	if !prevent || next == id || next == 0 {
		t.Fatal("fresh request required")
	}
	if a.fileClose.complete(id, true) {
		t.Fatal("old approval")
	}
	if !a.fileClose.complete(next, true) {
		t.Fatal("approval failed")
	}
	if a.EnableFileEditing() {
		t.Fatal("input must stay blocked before authorized quit")
	}
	if prevent, id := a.fileClose.request(); prevent || id != 0 {
		t.Fatal("authorized quit should pass")
	}
	if prevent, id := a.fileClose.request(); !prevent || id == 0 {
		t.Fatal("authorization must be one-shot")
	}
}

func TestFileCloseGuardConcurrentRequests(t *testing.T) {
	var g fileCloseGuard
	g.armed = true
	var wg sync.WaitGroup
	ids := make(chan uint64, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prevent, id := g.request()
			if !prevent {
				t.Error("unguarded request")
			}
			if id != 0 {
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)
	count := 0
	for range ids {
		count++
	}
	if count != 1 {
		t.Fatalf("emitted %d requests", count)
	}
}
