package codediagnostics

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FSWatcher is the injectable fsnotify seam (tests supply fake events).
type FSWatcher interface {
	Add(string) error
	Remove(string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type nativeFSWatcher struct{ w *fsnotify.Watcher }

func (n nativeFSWatcher) Add(p string) error            { return n.w.Add(p) }
func (n nativeFSWatcher) Remove(p string) error         { return n.w.Remove(p) }
func (n nativeFSWatcher) Close() error                  { return n.w.Close() }
func (n nativeFSWatcher) Events() <-chan fsnotify.Event { return n.w.Events }
func (n nativeFSWatcher) Errors() <-chan error          { return n.w.Errors }

func newNativeFSWatcher() (FSWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return nativeFSWatcher{w}, nil
}

// ProjectWatchOptions bound one attachment's watcher. Zero values take defaults;
// limits can only be narrowed.
type ProjectWatchOptions struct {
	NewFS    func() (FSWatcher, error)
	After    func(time.Duration) <-chan time.Time
	Now      func() time.Time
	Debounce time.Duration
	MaxWait  time.Duration
	// MaxDirs bounds watched directories; MaxEntries bounds the entries inside
	// them (kqueue holds one descriptor per watched directory entry).
	MaxDirs, MaxEntries, MaxPending int
	Limits                          GoSyntaxLimits
	// OnChanges receives analyzed saved-file transitions after publication, on
	// the watcher goroutine and outside Manager locks. It must be quick.
	// The context is canceled when the watcher stops. The initial scan is not
	// reported (it describes existing files, not transitions).
	OnChanges func(context.Context, []FileChange)
}

func (o ProjectWatchOptions) defaults() ProjectWatchOptions {
	narrow := func(v, max int) int {
		if v <= 0 || v > max {
			return max
		}
		return v
	}
	if o.NewFS == nil {
		o.NewFS = newNativeFSWatcher
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Debounce <= 0 || o.Debounce > time.Second {
		o.Debounce = 200 * time.Millisecond
	}
	if o.MaxWait <= 0 || o.MaxWait > 5*time.Second {
		o.MaxWait = 2 * time.Second
	}
	o.MaxDirs = narrow(o.MaxDirs, 4096)
	o.MaxEntries = narrow(o.MaxEntries, 16384)
	o.MaxPending = narrow(o.MaxPending, 1024)
	return o
}

// ProjectWatcher owns one goroutine and one fsnotify instance for one approved
// attachment context. It never reads outside the authorized root, never writes,
// and keeps no file bodies. Stop is idempotent and joins all owned work.
type ProjectWatcher struct {
	manager *Manager
	context Context
	root    AuthorizedRoot
	options ProjectWatchOptions
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	ctx     context.Context
	cancel  context.CancelFunc
	scanned bool // initial publication done
}

// StartProjectWatcher starts the asynchronous initial scan and watching. It
// returns immediately; nothing runs unless in-process analysis is approved for c.
func StartProjectWatcher(m *Manager, c Context, root AuthorizedRoot, options ProjectWatchOptions) *ProjectWatcher {
	w := &ProjectWatcher{manager: m, context: c, root: root, options: options.defaults(), stop: make(chan struct{}), done: make(chan struct{})}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	m.ownedWork.Add(1)
	go w.run()
	return w
}

func (w *ProjectWatcher) Context() Context { return w.context }

// Stop cancels the scan/watch and waits for the goroutine and fsnotify close.
func (w *ProjectWatcher) Stop() {
	w.once.Do(func() { close(w.stop); w.cancel() })
	<-w.done
}

// Done is closed after the watcher goroutine exits (stopped or failed).
func (w *ProjectWatcher) Done() <-chan struct{} { return w.done }

func (w *ProjectWatcher) stopped() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}

type watchState struct {
	fs       FSWatcher
	watched  map[string]bool // relative directory ("" root) → watched
	limited  bool            // directory/entry caps or add failures
	degraded string          // non-empty: not watching; findings may go stale
}

func (w *ProjectWatcher) run() {
	defer close(w.done)
	defer w.manager.ownedWork.Add(-1)
	defer w.cancel()
	if !w.manager.InProcessApproved(w.context, GoSyntaxProviderID) {
		return
	}
	g := NewGoSyntax(w.root, w.options.Limits)
	g.canceled = w.stopped
	st := &watchState{watched: map[string]bool{}}
	if fs, err := w.options.NewFS(); err == nil {
		st.fs = fs
		defer func() { _ = fs.Close() }()
	} else {
		st.degraded = "Project file watching is unavailable on this host; findings reflect the initial scan only."
	}
	if !w.full(g, st) {
		return
	}
	if st.fs == nil {
		return
	}
	var (
		pending = map[string]bool{}
		rescan  bool
		touched bool // a contained non-Go file changed: republish so clients reconcile
		first   time.Time
		timer   <-chan time.Time
	)
	queue := func() {
		now := w.options.Now()
		if first.IsZero() {
			first = now
		}
		if now.Sub(first) >= w.options.MaxWait {
			timer = closedTime
			return
		}
		timer = w.options.After(w.options.Debounce)
	}
	for {
		select {
		case <-w.stop:
			return
		case e, ok := <-st.fs.Events():
			if !ok {
				w.degrade("Project file watching stopped; findings may be stale.")
				return
			}
			rel, ok := w.relative(e.Name)
			if !ok {
				continue
			}
			goFile := strings.HasSuffix(rel, ".go")
			// Directory topology changes need a rescan to (un)watch subtrees.
			topology := (e.Has(fsnotify.Create) || e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename)) && (st.watched[rel] || (!goFile && w.isDirectory(rel)))
			switch {
			case topology:
				rescan = true
			case !goFile:
				if e.Op == fsnotify.Chmod || touched {
					continue
				}
				touched = true
			case !pending[rel] && len(pending) >= w.options.MaxPending:
				rescan = true // bounded queue overflow: coalesce into one rescan
			default:
				pending[rel] = true
			}
			queue()
		case err, ok := <-st.fs.Errors():
			if !ok {
				w.degrade("Project file watching stopped; findings may be stale.")
				return
			}
			// Includes fsnotify.ErrEventOverflow: events were lost, rescan.
			_ = err
			rescan = true
			queue()
		case <-timer:
			timer, first = nil, time.Time{}
			paths := make([]string, 0, len(pending))
			for p := range pending {
				paths = append(paths, p)
			}
			clear(pending)
			slices.Sort(paths)
			wasTouched := touched
			touched = false
			if rescan {
				rescan = false
				if !w.full(g, st) {
					return
				}
				continue
			}
			result, changes, err := g.Update(paths)
			if err != nil {
				w.degrade("Project root changed or is no longer authorized; watching stopped.")
				return
			}
			if len(changes) == 0 && !wasTouched {
				continue // metadata-only events: nothing observable changed
			}
			if !w.publish(result, st, changes) {
				return
			}
		}
	}
}

var closedTime = func() <-chan time.Time { c := make(chan time.Time); close(c); return c }()

// full rescans, synchronizes directory watches, and publishes.
func (w *ProjectWatcher) full(g *GoSyntax, st *watchState) bool {
	result, dirs, changes, err := g.Full()
	if w.stopped() {
		return false
	}
	if err != nil {
		w.degrade("Project root changed or is no longer authorized; watching stopped.")
		return false
	}
	if st.fs != nil && w.sync(st, dirs) {
		// Files created between the walk and a new directory's Add produced no
		// event; one bounded re-walk (cached by size/mtime) closes that window.
		again, dirs2, more, err := g.Full()
		if w.stopped() {
			return false
		}
		if err != nil {
			w.degrade("Project root changed or is no longer authorized; watching stopped.")
			return false
		}
		w.sync(st, dirs2)
		result, changes = again, mergeChanges(changes, more)
	}
	return w.publish(result, st, changes)
}

// sync reconciles directory watches with a walk; it reports whether any new
// directory watch was added.
func (w *ProjectWatcher) sync(st *watchState, dirs []walkDir) bool {
	added := false
	want := map[string]bool{}
	entries := 0
	st.limited = false
	for _, d := range dirs {
		if len(want) >= w.options.MaxDirs || entries+d.entries > w.options.MaxEntries {
			st.limited = true
			break
		}
		want[d.rel] = true
		entries += d.entries
	}
	for rel := range st.watched {
		if !want[rel] {
			_ = st.fs.Remove(w.absolute(rel)) // already gone after deletion
			delete(st.watched, rel)
		}
	}
	for _, d := range dirs {
		if !want[d.rel] || st.watched[d.rel] {
			continue
		}
		if err := st.fs.Add(w.absolute(d.rel)); err != nil {
			st.limited = true
			continue
		}
		st.watched[d.rel] = true
		added = true
	}
	return added
}

// mergeChanges keeps the latest kind per path (created-then-deleted cancels).
func mergeChanges(a, b []FileChange) []FileChange {
	kinds := map[string]int{}
	for _, c := range a {
		kinds[c.Path] = c.Kind
	}
	for _, c := range b {
		if kinds[c.Path] == 1 && c.Kind == 3 {
			delete(kinds, c.Path)
			continue
		}
		if kinds[c.Path] != 1 {
			kinds[c.Path] = c.Kind
		}
	}
	out := make([]FileChange, 0, len(kinds))
	for p, k := range kinds {
		out = append(out, FileChange{p, k})
	}
	slices.SortFunc(out, func(x, y FileChange) int { return strings.Compare(x.Path, y.Path) })
	return out
}

func (w *ProjectWatcher) publish(result CheckerResult, st *watchState, changes []FileChange) bool {
	external := "watching"
	if st.fs == nil || st.degraded != "" {
		external = "unwatched"
		result.Notice = goSyntaxNotice + " " + st.degraded
	}
	if st.limited {
		result.Partial = true
		result.Notice = goSyntaxNotice + " Watch limits reached; some directories are not watched and coverage is partial."
	}
	if err := w.manager.PublishInProcess(w.context, GoSyntaxProviderID, result, external); err != nil {
		return false // stale context, revoked or closed: never publish again
	}
	if len(changes) > 0 && w.scanned && w.options.OnChanges != nil && !w.stopped() {
		w.options.OnChanges(w.ctx, slices.Clone(changes))
	}
	w.scanned = true
	return true
}

func (w *ProjectWatcher) degrade(notice string) {
	_ = w.manager.InProcessStatus(w.context, GoSyntaxProviderID, goSyntaxNotice+" "+notice)
}

func (w *ProjectWatcher) absolute(rel string) string {
	return filepath.Join(w.root.Project().Root, filepath.FromSlash(rel))
}

// relative maps an event name to a contained, non-skipped portable path.
// Everything outside the canonical root (or in skipped trees) is dropped.
func (w *ProjectWatcher) relative(name string) (string, bool) {
	rel, err := filepath.Rel(w.root.Project().Root, filepath.Clean(name))
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if ValidatePath(rel) != nil || skippedPath(rel) {
		return "", false
	}
	if base := filepath.Base(rel); skipDirectory(base) {
		return "", false // includes Go-ignored "." and "_" files
	}
	return rel, true
}

// isDirectory never follows a symlink (Lstat through os.Root semantics).
func (w *ProjectWatcher) isDirectory(rel string) bool {
	r, err := os.OpenRoot(w.root.Project().Root)
	if err != nil {
		return false
	}
	defer func() { _ = r.Close() }()
	info, err := r.Lstat(filepath.FromSlash(rel))
	return err == nil && info.IsDir()
}
