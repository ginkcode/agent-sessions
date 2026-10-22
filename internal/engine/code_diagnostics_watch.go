package engine

import (
	"context"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

// syncWatcher starts the approved project watcher for one attachment, or stops
// it when the attachment is no longer approved. It is idempotent: a watcher for
// the same exact context is kept. Watchers are joined outside o.mu.
func (o *diagnosticsOwner) syncWatcher(id string) {
	o.watchMu.Lock()
	defer o.watchMu.Unlock()
	o.mu.Lock()
	c, attached := o.contexts[id]
	root := o.roots[id]
	current := o.watchers[id]
	closed := o.closed
	o.mu.Unlock()
	approved := attached && !closed && o.manager.InProcessApproved(c, cd.GoSyntaxProviderID)
	if current != nil {
		select {
		case <-current.Done():
			current = nil // ended (stale/degraded); a new approval may restart it
		default:
		}
	}
	if current != nil && approved && current.Context() == c {
		return
	}
	if current != nil {
		current.Stop()
		o.mu.Lock()
		delete(o.watchers, id)
		o.mu.Unlock()
	}
	if !approved {
		return
	}
	options := o.watch
	if o.live != nil {
		live := o.live
		options.OnChanges = func(parent context.Context, changes []cd.FileChange) {
			// Approved gopls session only: saved-file transitions, never text.
			ctx, cancel := context.WithTimeout(parent, 2*time.Second)
			defer cancel()
			_ = live.WatchedFiles(ctx, root, changes)
		}
	}
	w := cd.StartProjectWatcher(o.manager, c, root, options)
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		w.Stop()
		return
	}
	o.watchers[id] = w
	o.mu.Unlock()
}

// stopWatcher joins the attachment's watcher (revoke, detach).
func (o *diagnosticsOwner) stopWatcher(id string) {
	o.watchMu.Lock()
	defer o.watchMu.Unlock()
	o.mu.Lock()
	w := o.watchers[id]
	delete(o.watchers, id)
	o.mu.Unlock()
	if w != nil {
		w.Stop()
	}
}

// ProjectWatchers reports running project watchers (test-visible ownership).
func (o *diagnosticsOwner) projectWatchers() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, w := range o.watchers {
		select {
		case <-w.Done():
		default:
			n++
		}
	}
	return n
}
