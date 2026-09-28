// Package watch translates provider storage changes into coalesced refresh
// requests. It recursively watches directory-backed providers and watches the
// parent of replaceable database files.
package watch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

const (
	debounceDelay        = 300 * time.Millisecond
	debounceMaximum      = 2 * time.Second
	filePollInterval     = 30 * time.Second
	openCodePollInterval = 60 * time.Second
	fallbackPollInterval = 10 * time.Second
)

// Change identifies one provider's changed sources. Paths is nil when a full
// reconciliation is required (polling, a directory topology change, or a
// watcher error). Paths are absolute, cleaned, sorted, and unique.
type Change struct {
	Agent model.AgentID
	Paths []string
}

// Option configures a Watcher. The defaults are suitable for production.
type Option func(*options)

type options struct {
	newFSWatcher func() (fsWatcher, error)
	now          func() time.Time
	debounce     time.Duration
	maximum      time.Duration
	filePoll     time.Duration
	openCodePoll time.Duration
	fallbackPoll time.Duration
}

// WithIntervals overrides debounce and polling intervals. A zero duration
// retains the corresponding production default. It is primarily useful to
// make integration tests deterministic and fast.
func WithIntervals(debounce, maximum, filePoll, openCodePoll, fallbackPoll time.Duration) Option {
	return func(o *options) {
		if debounce > 0 {
			o.debounce = debounce
		}
		if maximum > 0 {
			o.maximum = maximum
		}
		if filePoll > 0 {
			o.filePoll = filePoll
		}
		if openCodePoll > 0 {
			o.openCodePoll = openCodePoll
		}
		if fallbackPoll > 0 {
			o.fallbackPoll = fallbackPoll
		}
	}
}

// Start begins watching provider storage. The callback is serialized by a
// single dispatcher; it must return promptly. Start succeeds in polling mode
// if fsnotify is unavailable. Calling the returned close function more than
// once is safe.
func Start(ctx context.Context, providers provider.Set, onChange func(Change), opts ...Option) (close func() error, err error) {
	watcher, err := New(providers, onChange, opts...)
	if err != nil {
		return nil, err
	}
	watcher.Start(ctx)
	return watcher.Close, nil
}

// Watcher owns provider watches, debounce state, and reconciliation polling.
type Watcher struct {
	providers provider.Set
	onChange  func(Change)
	options   options

	ctx    context.Context
	cancel context.CancelFunc
	fs     fsWatcher

	mu        sync.Mutex
	started   bool
	closed    bool
	closeErr  error
	states    map[model.AgentID]*providerState
	pathOwner map[string]map[model.AgentID]struct{}

	wake chan struct{}
	done chan struct{}
}

type providerState struct {
	provider provider.Provider
	pending  map[string]struct{}
	full     bool
	first    time.Time
	last     time.Time
	nextPoll time.Time
	fallback bool
	watched  map[string]struct{}
	roots    []watchRoot
}

type watchRoot struct {
	path      string
	recursive bool
	anchor    string
}

type fsWatcher interface {
	Add(string) error
	Remove(string) error
	Close() error
	WatchList() []string
	EventsChan() <-chan fsnotify.Event
	ErrorsChan() <-chan error
}

type nativeWatcher struct {
	*fsnotify.Watcher
}

func (w *nativeWatcher) EventsChan() <-chan fsnotify.Event { return w.Events }
func (w *nativeWatcher) ErrorsChan() <-chan error          { return w.Errors }

// New constructs an unstarted Watcher. Use Start to bind its lifetime to a
// context. Duplicate provider IDs and nil providers are rejected.
func New(providers provider.Set, onChange func(Change), opts ...Option) (*Watcher, error) {
	if onChange == nil {
		return nil, errors.New("watch: onChange is nil")
	}
	cfg := options{
		newFSWatcher: func() (fsWatcher, error) {
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				return nil, err
			}
			return &nativeWatcher{Watcher: watcher}, nil
		},
		now:          time.Now,
		debounce:     debounceDelay,
		maximum:      debounceMaximum,
		filePoll:     filePollInterval,
		openCodePoll: openCodePollInterval,
		fallbackPoll: fallbackPollInterval,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	states := make(map[model.AgentID]*providerState, len(providers))
	for _, p := range providers {
		if p == nil {
			return nil, errors.New("watch: provider is nil")
		}
		if _, exists := states[p.ID()]; exists {
			return nil, fmt.Errorf("watch: duplicate provider %q", p.ID())
		}
		states[p.ID()] = &providerState{
			provider: p,
			pending:  make(map[string]struct{}),
			watched:  make(map[string]struct{}),
		}
	}

	return &Watcher{
		providers: providers,
		onChange:  onChange,
		options:   cfg,
		states:    states,
		pathOwner: make(map[string]map[model.AgentID]struct{}),
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}, nil
}

// Start starts the watcher once. A cancelled parent context cleanly shuts it
// down; Close can still be called afterwards.
func (w *Watcher) Start(parent context.Context) {
	w.mu.Lock()
	if w.started || w.closed {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.ctx, w.cancel = context.WithCancel(parent)
	now := w.options.now()
	for _, state := range w.states {
		state.roots = rootsFor(state.provider)
		state.nextPoll = now.Add(w.pollInterval(state))
	}

	watcher, err := w.options.newFSWatcher()
	if err != nil {
		for _, state := range w.states {
			w.setFallbackLocked(state, now)
		}
	} else {
		w.fs = watcher
		for _, state := range w.states {
			w.installProviderLocked(state, now)
		}
	}
	w.mu.Unlock()

	go w.run()
}

// Close stops all watches and polling. It is idempotent and waits until no
// callback from this watcher can start.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		err := w.closeErr
		done := w.done
		started := w.started
		w.mu.Unlock()
		if started {
			<-done
		}
		return err
	}
	w.closed = true
	if w.cancel != nil {
		w.cancel()
	}
	if w.fs != nil {
		w.closeErr = w.fs.Close()
	}
	err := w.closeErr
	done := w.done
	started := w.started
	w.mu.Unlock()
	if started {
		<-done
	} else {
		close(done)
	}
	return err
}

func (w *Watcher) run() {
	defer close(w.done)

	timer := time.NewTimer(w.nextDelay())
	defer timer.Stop()

	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	w.mu.Lock()
	if w.fs != nil {
		events = w.fs.EventsChan()
		watchErrors = w.fs.ErrorsChan()
	}
	w.mu.Unlock()

	for {
		select {
		case <-w.ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				w.failAll()
				continue
			}
			w.handleEvent(event)
		case _, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				w.failAll()
				continue
			}
			w.failAll()
		case <-w.wake:
		case <-timer.C:
		}

		w.dispatchDue()
		resetTimer(timer, w.nextDelay())
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	path, ok := absoluteClean(event.Name)
	if !ok {
		return
	}
	now := w.options.now()

	w.mu.Lock()
	defer w.mu.Unlock()
	owners := w.ownersLocked(path)
	for id := range owners {
		state := w.states[id]
		if state == nil || state.fallback {
			continue
		}

		isDir := false
		if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				isDir = true
				if err := w.addTreeLocked(state, path); err != nil {
					w.setFallbackLocked(state, now)
					continue
				}
			}
		}

		// Whether the removed path was a watched directory must be captured
		// before forgetTreeLocked drops it from the watch bookkeeping.
		wasWatchedDir := false
		if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			wasWatchedDir = w.isWatchedDirLocked(state, path)
			w.forgetTreeLocked(state, path)
		}
		if isDir || wasWatchedDir {
			w.queueLocked(state, nil, true, now)
			continue
		}

		matched, full := relevantEvent(state.provider.ID(), path, event.Op)
		if !matched {
			continue
		}
		if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			full = true
		}
		if full {
			w.queueLocked(state, nil, true, now)
		} else {
			w.queueLocked(state, []string{path}, false, now)
		}
	}
}

func (w *Watcher) failAll() {
	now := w.options.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, state := range w.states {
		w.setFallbackLocked(state, now)
	}
}

func (w *Watcher) installProviderLocked(state *providerState, now time.Time) {
	for _, root := range state.roots {
		if err := w.installRootLocked(state, root); err != nil {
			w.setFallbackLocked(state, now)
			return
		}
	}
}

func (w *Watcher) installRootLocked(state *providerState, root watchRoot) error {
	if root.path == "" {
		return nil
	}
	info, err := os.Stat(root.path)
	switch {
	case err == nil && info.IsDir():
		if root.recursive {
			return w.addTreeLocked(state, root.path)
		}
		return w.addDirLocked(state, root.path)
	case err == nil:
		return w.addDirLocked(state, filepath.Dir(root.path))
	case errors.Is(err, fs.ErrNotExist):
		anchor := root.anchor
		if anchor == "" {
			anchor = nearestExistingDir(root.path)
		}
		if anchor == "" {
			return nil
		}
		return w.addDirLocked(state, anchor)
	default:
		return err
	}
}

func (w *Watcher) addTreeLocked(state *providerState, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		return w.addDirLocked(state, path)
	})
}

func (w *Watcher) addDirLocked(state *providerState, path string) error {
	path, ok := absoluteClean(path)
	if !ok {
		return nil
	}
	if _, exists := state.watched[path]; exists {
		return nil
	}
	if w.fs == nil {
		return errors.New("watch: filesystem watcher unavailable")
	}
	if err := w.fs.Add(path); err != nil {
		return err
	}
	state.watched[path] = struct{}{}
	owners := w.pathOwner[path]
	if owners == nil {
		owners = make(map[model.AgentID]struct{})
		w.pathOwner[path] = owners
	}
	owners[state.provider.ID()] = struct{}{}
	return nil
}

func (w *Watcher) forgetTreeLocked(state *providerState, path string) {
	prefix := path + string(filepath.Separator)
	for watched := range state.watched {
		if watched != path && !strings.HasPrefix(watched, prefix) {
			continue
		}
		delete(state.watched, watched)
		owners := w.pathOwner[watched]
		delete(owners, state.provider.ID())
		if len(owners) == 0 {
			delete(w.pathOwner, watched)
		}
	}
}

func (w *Watcher) isWatchedDirLocked(state *providerState, path string) bool {
	_, ok := state.watched[path]
	return ok
}

func (w *Watcher) ownersLocked(path string) map[model.AgentID]struct{} {
	owners := make(map[model.AgentID]struct{})
	dir := filepath.Dir(path)
	for id := range w.pathOwner[dir] {
		owners[id] = struct{}{}
	}
	for id := range w.pathOwner[path] {
		owners[id] = struct{}{}
	}
	return owners
}

func (w *Watcher) setFallbackLocked(state *providerState, now time.Time) {
	if state.fallback {
		return
	}
	state.fallback = true
	state.nextPoll = now.Add(w.options.fallbackPoll)
	for path := range state.watched {
		owners := w.pathOwner[path]
		delete(owners, state.provider.ID())
		if len(owners) == 0 {
			delete(w.pathOwner, path)
			if w.fs != nil {
				_ = w.fs.Remove(path)
			}
		}
	}
	clear(state.watched)
	w.signalWake()
}

func (w *Watcher) queueLocked(state *providerState, paths []string, full bool, now time.Time) {
	if state.first.IsZero() {
		state.first = now
	}
	state.last = now
	if full {
		state.full = true
		clear(state.pending)
	} else if !state.full {
		for _, path := range paths {
			if clean, ok := absoluteClean(path); ok {
				state.pending[clean] = struct{}{}
			}
		}
	}
	w.signalWake()
}

func (w *Watcher) signalWake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Watcher) dispatchDue() {
	now := w.options.now()
	var changes []Change

	w.mu.Lock()
	for _, p := range w.providers {
		state := w.states[p.ID()]
		if !state.nextPoll.After(now) {
			w.queueLocked(state, nil, true, now)
			state.nextPoll = now.Add(w.pollInterval(state))
		}
		if state.first.IsZero() {
			continue
		}
		if now.Before(state.last.Add(w.options.debounce)) && now.Before(state.first.Add(w.options.maximum)) {
			continue
		}
		change := Change{Agent: p.ID()}
		if !state.full {
			change.Paths = make([]string, 0, len(state.pending))
			for path := range state.pending {
				change.Paths = append(change.Paths, path)
			}
			sort.Strings(change.Paths)
		}
		state.full = false
		state.first = time.Time{}
		state.last = time.Time{}
		clear(state.pending)
		changes = append(changes, change)
	}
	w.mu.Unlock()

	for _, change := range changes {
		select {
		case <-w.ctx.Done():
			return
		default:
			w.onChange(change)
		}
	}
}

func (w *Watcher) nextDelay() time.Duration {
	now := w.options.now()
	delay := time.Hour

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, state := range w.states {
		if until := state.nextPoll.Sub(now); until < delay {
			delay = until
		}
		if !state.first.IsZero() {
			due := state.last.Add(w.options.debounce)
			if maxDue := state.first.Add(w.options.maximum); maxDue.Before(due) {
				due = maxDue
			}
			if until := due.Sub(now); until < delay {
				delay = until
			}
		}
	}
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

func (w *Watcher) pollInterval(state *providerState) time.Duration {
	if state.fallback {
		return w.options.fallbackPoll
	}
	if state.provider.ID() == model.AgentOpenCode {
		return w.options.openCodePoll
	}
	return w.options.filePoll
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func relevantEvent(agent model.AgentID, path string, op fsnotify.Op) (matched, full bool) {
	if op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) == 0 {
		return false, false
	}
	name := filepath.Base(path)
	switch agent {
	case model.AgentClaude:
		if strings.HasSuffix(name, ".jsonl") {
			return true, false
		}
		if strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".meta.json") {
			return true, true
		}
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".key") && filepath.Base(filepath.Dir(path)) == "sessions" {
			return true, true
		}
	case model.AgentCodex:
		if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
			return true, false
		}
	case model.AgentOpenCode:
		if name == "opencode.db" || name == "opencode.db-wal" {
			return true, true
		}
		if strings.HasSuffix(name, ".json") && isLegacyOpenCodePath(path) {
			return true, true
		}
	}
	return false, false
}

func rootsFor(p provider.Provider) []watchRoot {
	paths := p.WatchPaths()
	roots := make([]watchRoot, 0, len(paths)+1)
	seen := make(map[string]struct{})
	for _, raw := range paths {
		path, ok := absoluteClean(raw)
		if !ok {
			continue
		}
		root := watchRoot{path: path}
		switch p.ID() {
		case model.AgentClaude:
			root.recursive = true
		case model.AgentCodex:
			base := filepath.Base(path)
			root.recursive = base == "sessions" || base == "archived_sessions"
			if !root.recursive {
				root.path = filepath.Dir(path)
			}
		case model.AgentOpenCode:
			if base := filepath.Base(path); base == "opencode.db" || base == "opencode.db-wal" {
				root.path = filepath.Dir(path)
			} else {
				root.recursive = true
			}
		}
		if _, exists := seen[root.path]; exists {
			continue
		}
		seen[root.path] = struct{}{}
		roots = append(roots, root)
	}
	return roots
}

func isLegacyOpenCodePath(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "storage" && (parts[i+1] == "session" || parts[i+1] == "message" || parts[i+1] == "part") {
			return true
		}
	}
	return false
}

func absoluteClean(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(absolute), true
}

func nearestExistingDir(path string) string {
	path = filepath.Clean(path)
	for {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func isWatchLimit(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE)
}
