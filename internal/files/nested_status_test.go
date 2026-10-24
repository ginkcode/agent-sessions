package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWalkReportsNestedRepositoryStatus(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	write(t, root, "notes.md", "plain\n")
	nestedRepo(t, root, "api")
	write(t, root, "api/main.go", "package main\n\nfunc main() {}\n")
	write(t, root, "api/new.txt", "new\n")
	nestedRepo(t, root, "clean")
	nestedRepo(t, root, "apps/web")
	write(t, root, "apps/web/inner/.git/HEAD", "not a repository\n")
	if err := os.Remove(filepath.Join(root, "apps/web/main.go")); err != nil {
		t.Fatal(err)
	}

	calls := map[string]int{}
	var mu sync.Mutex
	ctx := context.WithValue(context.Background(), gitInvocationKey{}, func(c string) { mu.Lock(); calls[c]++; mu.Unlock() })
	got, err := walk(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{"api/main.go": Modified, "api/new.txt": Untracked, "apps/web/main.go": Deleted}
	if len(got.Status) != len(want) {
		t.Errorf("status = %v, want %v", got.Status, want)
	}
	for path, s := range want {
		if got.Status[path] != s {
			t.Errorf("status[%s] = %q, want %q (all: %v)", path, got.Status[path], s, got.Status)
		}
	}
	// One status per repository found (api, apps/web, apps/web/inner, clean); nothing else.
	if calls["status"] != 4 || len(calls) != 1 {
		t.Errorf("git work = %v", calls)
	}
}

func TestNestedStatusNeverReportsAnOuterRepository(t *testing.T) {
	requireGit(t)
	outer := t.TempDir()
	nestedRepo(t, outer, "ws")
	write(t, outer, "ws/changed.txt", "untracked in the outer repository\n")
	// Walked directly, as if ws were not a work tree: bad has an invalid .git.
	write(t, outer, "ws/bad/.git/HEAD", "not a repository\n")
	write(t, outer, "ws/bad/file.txt", "x\n")
	got, err := walk(context.Background(), filepath.Join(outer, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	for path := range got.Status {
		if strings.HasPrefix(path, "bad/") {
			t.Errorf("invalid nested .git reported the outer repository: %v", got.Status)
		}
	}
}

func TestNestedStatusesAreBoundedAndBestEffort(t *testing.T) {
	saved, savedGrace := nestedStatusOf, nestedStatusGrace
	t.Cleanup(func() { nestedStatusOf, nestedStatusGrace = saved, savedGrace })
	var running, peak atomic.Int32
	var slowExited atomic.Int32
	nestedStatusOf = func(ctx context.Context, dir string) map[string]Status {
		n := running.Add(1)
		defer running.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		switch filepath.Base(dir) {
		case "slow":
			<-ctx.Done()
			slowExited.Add(1)
			return map[string]Status{"late.go": Modified}
		case "broken":
			return nil
		}
		time.Sleep(10 * time.Millisecond)
		return map[string]Status{"a.go": Modified}
	}

	n := startNestedStatuses(context.Background())
	for i := range 12 {
		n.start("/r/fast", "fast"+string(rune('a'+i)))
	}
	n.start("/r/broken", "broken")
	n.start("/r/slow", "slow")
	began := time.Now()
	got := n.finish(300 * time.Millisecond)
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Fatalf("finish took %v, want about the grace", elapsed)
	}
	if peak.Load() > nestedStatusWorkers {
		t.Errorf("peak concurrent statuses = %d, want at most %d", peak.Load(), nestedStatusWorkers)
	}
	if slowExited.Load() != 1 || running.Load() != 0 {
		t.Errorf("slow status not cancelled before finish returned: exited=%d running=%d", slowExited.Load(), running.Load())
	}
	if len(got) != 12 || got["fasta/a.go"] != Modified || got["slow/late.go"] != "" {
		t.Errorf("status = %v, want the 12 fast repositories only", got)
	}

	// None found or all failed: no status at all, as before.
	if got := startNestedStatuses(context.Background()).finish(time.Second); got != nil {
		t.Errorf("empty status = %v, want nil", got)
	}
	// A cancelled listing stops waiting at once.
	ctx, cancel := context.WithCancel(context.Background())
	n = startNestedStatuses(ctx)
	n.start("/r/slow", "slow")
	cancel()
	if got := n.finish(time.Hour); got != nil {
		t.Errorf("cancelled status = %v, want nil", got)
	}
}
