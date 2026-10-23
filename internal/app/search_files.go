package app

import (
	"context"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/files"
)

// fileSearches holds the content searches in progress, by the ID the
// frontend gave each.
type fileSearches struct {
	mu      sync.Mutex
	running map[string]*fileSearch
}

type fileSearch struct{ cancel context.CancelFunc }

// start registers a search and returns its context and the function that
// ends it. A search already running under the same ID is canceled: the
// frontend reuses an ID for the search that replaces it.
func (s *fileSearches) start(parent context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	if id == "" {
		return ctx, cancel
	}
	run := &fileSearch{cancel: cancel}
	s.mu.Lock()
	if s.running == nil {
		s.running = make(map[string]*fileSearch)
	}
	if prev := s.running[id]; prev != nil {
		prev.cancel()
	}
	s.running[id] = run
	s.mu.Unlock()
	return ctx, func() {
		cancel()
		s.mu.Lock()
		// A newer search may have taken the ID; leave that one registered.
		if s.running[id] == run {
			delete(s.running, id)
		}
		s.mu.Unlock()
	}
}

// cancel stops the search with the given ID, if it is still running.
func (s *fileSearches) cancel(id string) {
	s.mu.Lock()
	run := s.running[id]
	delete(s.running, id)
	s.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

// cancelAll stops every search in progress.
func (s *fileSearches) cancelAll() {
	s.mu.Lock()
	running := s.running
	s.running = nil
	s.mu.Unlock()
	for _, run := range running {
		run.cancel()
	}
}

// SearchFiles searches the text of a session directory's files on the
// selected host. searchID names the search for CancelSearch; a new search
// under the same ID cancels the old one, and a host switch cancels them all.
func (a *App) SearchFiles(dir string, q files.SearchQuery, searchID string) (files.SearchResult, error) {
	ctx, done := a.searches.start(a.appCtx(), searchID)
	defer done()
	return a.activeBackend().SearchFiles(ctx, dir, q)
}

// CancelSearch stops the content search started under searchID. Its
// SearchFiles call returns a cancellation error.
func (a *App) CancelSearch(searchID string) {
	a.searches.cancel(searchID)
}
