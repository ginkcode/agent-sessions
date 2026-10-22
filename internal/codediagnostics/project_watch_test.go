package codediagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

type fakeFS struct {
	mu      sync.Mutex
	events  chan fsnotify.Event
	errors  chan error
	added   map[string]bool
	closed  bool
	failAdd bool
}

func newFakeFS() *fakeFS {
	return &fakeFS{events: make(chan fsnotify.Event, 64), errors: make(chan error, 4), added: map[string]bool{}}
}
func (f *fakeFS) Add(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAdd {
		return errors.New("no")
	}
	f.added[p] = true
	return nil
}
func (f *fakeFS) Remove(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.added, p)
	return nil
}
func (f *fakeFS) Close() error                  { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }
func (f *fakeFS) Events() <-chan fsnotify.Event { return f.events }
func (f *fakeFS) Errors() <-chan error          { return f.errors }
func (f *fakeFS) watching(p string) bool        { f.mu.Lock(); defer f.mu.Unlock(); return f.added[p] }

// approvedSyntax returns a Manager with an approved attachment carrying the
// in-process go-syntax provider for root.
func approvedSyntax(t *testing.T, root AuthorizedRoot) (*Manager, Snapshot) {
	t.Helper()
	dir := root.Project().Root
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte("synthetic tool"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := []ToolInput{{ProviderID: "test", Executable: filepath.Join(dir, "tool"), Policy: "adapter-v1"}}
	trust := NewTrust(nil)
	m, err := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	s, err := m.Attach(root, 1, []Registration{{ProviderID: "checker", Availability: "available", Adapter: &fakeChecker{tools, make(chan adapterCall, 1)}}, {ProviderID: GoSyntaxProviderID, Availability: "available", InProcess: true}})
	if err != nil {
		t.Fatal(err)
	}
	if s, err = m.Approve(s.Context, s.Proposal.Fingerprint, "session"); err != nil {
		t.Fatal(err)
	}
	return m, s
}

func syntaxSet(s Snapshot) (ResultSet, ProviderState) {
	var set ResultSet
	for _, r := range s.ResultSets {
		if r.ProviderID == GoSyntaxProviderID {
			set = r
		}
	}
	var p ProviderState
	for _, q := range s.Providers {
		if q.ProviderID == GoSyntaxProviderID {
			p = q
		}
	}
	return set, p
}

func paths(set ResultSet) []string {
	var out []string
	for _, d := range set.Diagnostics {
		if !slices.Contains(out, d.Path) {
			out = append(out, d.Path)
		}
	}
	return out
}

func TestProjectWatcherFakeEventsDebounceAndCleanup(t *testing.T) {
	root, dir := syntaxRoot(t, map[string]string{"a.go": "package a\nfunc {", "sub/ok.go": "package sub\n"})
	m, s := approvedSyntax(t, root)
	fs := newFakeFS()
	timer := make(chan time.Time)
	fire := func() {
		// The send completes only once the loop selects it, i.e. after every
		// buffered event was dequeued and processed.
		for len(fs.events) > 0 || len(fs.errors) > 0 {
			time.Sleep(time.Millisecond)
		}
		timer <- time.Now()
	}
	var mu sync.Mutex
	var notified [][]FileChange
	w := StartProjectWatcher(m, s.Context, root, ProjectWatchOptions{
		NewFS: func() (FSWatcher, error) { return fs, nil },
		After: func(time.Duration) <-chan time.Time { return timer },
		OnChanges: func(_ context.Context, c []FileChange) {
			mu.Lock()
			notified = append(notified, c)
			mu.Unlock()
		},
	})
	snap := awaitSnapshot(t, m, s.Context, func(s Snapshot) bool { set, _ := syntaxSet(s); return len(set.Diagnostics) > 0 })
	set, p := syntaxSet(snap)
	if !slices.Equal(paths(set), []string{"a.go"}) || p.Coverage.ExternalChanges != "watching" || p.Availability != "available" {
		t.Fatalf("initial %+v %+v", set, p)
	}
	if !fs.watching(dir) || !fs.watching(filepath.Join(dir, "sub")) {
		t.Fatal("directories not watched")
	}
	// Fix a.go, break sub/ok.go, and emit noisy plus out-of-root events. Only
	// one debounced update follows the single timer fire.
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "sub", "ok.go"), []byte("package sub\nvar = \n"), 0o600)
	for _, e := range []fsnotify.Event{
		{Name: filepath.Join(dir, "a.go"), Op: fsnotify.Write},
		{Name: filepath.Join(dir, "a.go"), Op: fsnotify.Write},
		{Name: filepath.Join(dir, "sub", "ok.go"), Op: fsnotify.Write},
		{Name: filepath.Join(filepath.Dir(dir), "outside.go"), Op: fsnotify.Write},
		{Name: filepath.Join(dir, ".git", "index"), Op: fsnotify.Write},
	} {
		fs.events <- e
	}
	before := snap.Sequence
	fire()
	snap = awaitSnapshot(t, m, s.Context, func(s Snapshot) bool { return s.Sequence > before })
	set, _ = syntaxSet(snap)
	if !slices.Equal(paths(set), []string{"sub/ok.go"}) {
		t.Fatalf("after change %v", paths(set))
	}
	// Directory creation is topology: rescan watches it and finds its file.
	_ = os.MkdirAll(filepath.Join(dir, "new"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "new", "n.go"), []byte("package n\nfunc {"), 0o600)
	fs.events <- fsnotify.Event{Name: filepath.Join(dir, "new"), Op: fsnotify.Create}
	before = snap.Sequence
	fire()
	snap = awaitSnapshot(t, m, s.Context, func(s Snapshot) bool { return s.Sequence > before })
	set, _ = syntaxSet(snap)
	if !slices.Contains(paths(set), "new/n.go") || !fs.watching(filepath.Join(dir, "new")) {
		t.Fatalf("new directory not rescanned/watched: %v", paths(set))
	}
	// Overflow (lost events) triggers a full rescan rather than claiming clean.
	_ = os.Remove(filepath.Join(dir, "new", "n.go"))
	fs.errors <- fsnotify.ErrEventOverflow
	before = snap.Sequence
	fire()
	snap = awaitSnapshot(t, m, s.Context, func(s Snapshot) bool { return s.Sequence > before })
	if set, _ = syntaxSet(snap); slices.Contains(paths(set), "new/n.go") {
		t.Fatal("rescan kept deleted file")
	}
	mu.Lock()
	if len(notified) == 0 {
		t.Fatal("changes were not forwarded")
	}
	for _, batch := range notified {
		for _, c := range batch {
			if strings.Contains(c.Path, "outside") || strings.HasPrefix(c.Path, ".git") {
				t.Fatalf("uncontained change forwarded: %v", c)
			}
		}
	}
	mu.Unlock()
	w.Stop()
	w.Stop()
	fs.mu.Lock()
	closed := fs.closed
	fs.mu.Unlock()
	if !closed || m.OwnedWorkers() != 0 {
		t.Fatalf("cleanup: closed=%v workers=%d", closed, m.OwnedWorkers())
	}
}

func TestProjectWatcherStopsOnRevokeAndDegradesHonestly(t *testing.T) {
	root, _ := syntaxRoot(t, map[string]string{"a.go": "package a\nfunc {"})
	m, s := approvedSyntax(t, root)
	// Unavailable fsnotify: one scan, reported unwatched/partial, never "watching".
	w := StartProjectWatcher(m, s.Context, root, ProjectWatchOptions{NewFS: func() (FSWatcher, error) { return nil, errors.New("unsupported") }})
	<-w.Done()
	snap, _ := m.GetSnapshot(s.Context)
	set, p := syntaxSet(snap)
	if len(set.Diagnostics) == 0 || p.Coverage.ExternalChanges != "unwatched" || !p.Coverage.Partial {
		t.Fatalf("degraded %+v", p)
	}
	// Watch add failures are partial coverage.
	fs := newFakeFS()
	fs.failAdd = true
	w = StartProjectWatcher(m, s.Context, root, ProjectWatchOptions{NewFS: func() (FSWatcher, error) { return fs, nil }})
	before := snap.Sequence
	snap = awaitSnapshot(t, m, s.Context, func(s Snapshot) bool { return s.Sequence > before })
	if _, p = syntaxSet(snap); !p.Coverage.Partial {
		t.Fatal("failed watch registration not partial")
	}
	// Closing the event stream stales retained findings.
	close(fs.events)
	<-w.Done()
	snap, _ = m.GetSnapshot(s.Context)
	if set, p = syntaxSet(snap); p.Coverage.ExternalChanges != "unwatched" || p.Failure == nil || set.Diagnostics[0].Freshness != "stale" {
		t.Fatalf("stopped watcher not stale: %+v", p)
	}
	// After revoke the watcher cannot publish and exits by itself.
	fs = newFakeFS()
	timer := make(chan time.Time)
	w = StartProjectWatcher(m, s.Context, root, ProjectWatchOptions{NewFS: func() (FSWatcher, error) { return fs, nil }, After: func(time.Duration) <-chan time.Time { return timer }})
	before = snap.Sequence
	awaitSnapshot(t, m, s.Context, func(s Snapshot) bool { return s.Sequence > before })
	revoked, _ := m.Revoke(s.Context)
	fs.events <- fsnotify.Event{Name: filepath.Join(root.Project().Root, "a.go"), Op: fsnotify.Write}
	timer <- time.Now()
	<-w.Done()
	after, _ := m.GetSnapshot(revoked.Context)
	if len(after.ResultSets) != 0 || after.Sequence != revoked.Sequence {
		t.Fatalf("published after revoke: %+v", after)
	}
	// A watcher started for an unapproved context never scans.
	w = StartProjectWatcher(m, revoked.Context, root, ProjectWatchOptions{NewFS: func() (FSWatcher, error) { t.Error("fsnotify created without approval"); return nil, errors.New("x") }})
	<-w.Done()
	if m.OwnedWorkers() != 0 {
		t.Fatal("owned work leaked")
	}
}

// Real fsnotify on a temp dir: unopened syntax error → fix → create → delete.
func TestProjectWatcherRealFSNotify(t *testing.T) {
	root, dir := syntaxRoot(t, map[string]string{"pkg/a.go": "package pkg\nfunc broken( {\n", "README.md": "x"})
	m, s := approvedSyntax(t, root)
	w := StartProjectWatcher(m, s.Context, root, ProjectWatchOptions{Debounce: 20 * time.Millisecond})
	defer w.Stop()
	wait := func(what string, ok func([]string) bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			snap, err := m.GetSnapshot(s.Context)
			if err != nil {
				t.Fatal(err)
			}
			set, p := syntaxSet(snap)
			if set.RunID != "" && p.Coverage.ExternalChanges == "watching" && ok(paths(set)) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		snap, _ := m.GetSnapshot(s.Context)
		set, p := syntaxSet(snap)
		t.Fatalf("%s: %v %+v", what, paths(set), p)
	}
	wait("initial error", func(p []string) bool { return slices.Equal(p, []string{"pkg/a.go"}) })
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/a.go", "package pkg\nfunc fixed() {}\n")
	wait("fixed", func(p []string) bool { return len(p) == 0 })
	write("pkg/b.go", "package pkg\nvar = 1\n")
	wait("created", func(p []string) bool { return slices.Equal(p, []string{"pkg/b.go"}) })
	if err := os.MkdirAll(filepath.Join(dir, "later"), 0o700); err != nil {
		t.Fatal(err)
	}
	write("later/c.go", "package later\nfunc {\n")
	wait("new directory", func(p []string) bool { return slices.Equal(p, []string{"later/c.go", "pkg/b.go"}) })
	if err := os.Remove(filepath.Join(dir, "pkg", "b.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "later")); err != nil {
		t.Fatal(err)
	}
	wait("deleted", func(p []string) bool { return len(p) == 0 })
	w.Stop()
	if m.OwnedWorkers() != 0 {
		t.Fatalf("owned workers %d", m.OwnedWorkers())
	}
}
