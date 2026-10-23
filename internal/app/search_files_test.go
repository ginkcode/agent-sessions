package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

// blockingSearchBackend reports each search it starts and holds it until
// the search's context ends.
type blockingSearchBackend struct {
	engine.Backend
	started chan files.SearchQuery
}

func (b *blockingSearchBackend) SearchFiles(ctx context.Context, _ string, q files.SearchQuery) (files.SearchResult, error) {
	b.started <- q
	<-ctx.Done()
	return files.SearchResult{}, ctx.Err()
}

func runSearch(a *App, q files.SearchQuery, id string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := a.SearchFiles("/repo", q, id)
		done <- err
	}()
	return done
}

func wantCanceled(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search was not canceled")
	}
}

func TestCancelSearchByID(t *testing.T) {
	b := &blockingSearchBackend{started: make(chan files.SearchQuery, 4)}
	a := &App{backend: b}

	first := runSearch(a, files.SearchQuery{Pattern: "one"}, "pane")
	<-b.started
	other := runSearch(a, files.SearchQuery{Pattern: "other"}, "elsewhere")
	<-b.started

	// A new search under the same ID replaces the old one.
	second := runSearch(a, files.SearchQuery{Pattern: "two"}, "pane")
	<-b.started
	wantCanceled(t, first)

	a.CancelSearch("pane")
	wantCanceled(t, second)
	select {
	case err := <-other:
		t.Fatalf("search under another ID ended: %v", err)
	default:
	}
	a.CancelSearch("elsewhere")
	wantCanceled(t, other)
	a.CancelSearch("unknown") // no search, no effect

	if n := len(a.searches.running); n != 0 {
		t.Errorf("%d searches still registered", n)
	}
}

func TestHostSwitchCancelsSearches(t *testing.T) {
	b := &blockingSearchBackend{started: make(chan files.SearchQuery, 2)}
	a := &App{backend: b}
	first := runSearch(a, files.SearchQuery{Pattern: "one"}, "a")
	second := runSearch(a, files.SearchQuery{Pattern: "two"}, "b")
	<-b.started
	<-b.started
	a.Disconnect()
	wantCanceled(t, first)
	wantCanceled(t, second)
}
