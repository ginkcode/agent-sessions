package files

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// nestedStatusWorkers bounds the git status processes a walk runs at once.
const nestedStatusWorkers = 4

// nestedStatusGrace is how long a walked listing waits, after its walk, for
// the statuses of the repositories found below it. Slower ones are cancelled
// and left without status rather than delaying the listing.
var nestedStatusGrace = 2 * time.Second

// nestedStatusOf returns a nested repository's status by path from its top.
// Tests replace it.
var nestedStatusOf = repositoryStatus

// nestedStatuses collects, best effort, the git status of the repositories a
// walk finds. Each starts as soon as it is found, so the work overlaps the
// walk.
type nestedStatuses struct {
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	status map[string]Status
}

func startNestedStatuses(ctx context.Context) *nestedStatuses {
	ctx, cancel := context.WithCancel(ctx)
	return &nestedStatuses{ctx: ctx, cancel: cancel, slots: make(chan struct{}, nestedStatusWorkers), status: map[string]Status{}}
}

// start queues the status of the work tree dir, whose listing path is rel.
func (n *nestedStatuses) start(dir, rel string) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		select {
		case n.slots <- struct{}{}:
		case <-n.ctx.Done():
			return
		}
		defer func() { <-n.slots }()
		got := nestedStatusOf(n.ctx, dir)
		if n.ctx.Err() != nil {
			return
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		for path, s := range got {
			n.status[rel+"/"+path] = s
		}
	}()
}

// finish waits up to grace for the statuses, cancels the rest and returns
// what arrived, or nil for none. No goroutine or git process outlives it.
func (n *nestedStatuses) finish(grace time.Duration) map[string]Status {
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-n.ctx.Done():
	}
	n.cancel()
	<-done
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.status) == 0 {
		return nil
	}
	return n.status
}

// repositoryStatus is gitStatus for a work tree whose top is dir. Discovery
// may not go above dir, so an invalid .git never reports an outer
// repository's files. Nested work trees git does not enter are left out.
func repositoryStatus(ctx context.Context, dir string) map[string]Status {
	env := []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir)}
	out, err := gitOutputEnv(ctx, dir, nil, env, []string{"--literal-pathspecs"}, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil
	}
	status := parseStatus(string(out), "")
	for path := range status {
		if strings.HasSuffix(path, "/") {
			delete(status, path)
		}
	}
	return status
}
