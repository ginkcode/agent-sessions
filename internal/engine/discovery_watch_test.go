package engine

import (
	"context"
	"testing"
)

func TestWatchDiscoveryLifecycle(t *testing.T) {
	e := NewEngine()
	initial, changed := e.WatchDiscovery()
	if initial.Lifetime != 0 || initial.State != DiscoveryInitializing {
		t.Fatalf("initial=%+v", initial)
	}
	assertClosed := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		default:
			t.Fatal("observer not woken")
		}
	}
	assertOpen := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
			t.Fatal("observer closed prematurely")
		default:
		}
	}
	ctx := e.beginDiscovery(context.Background())
	assertClosed(changed)
	s, changed := e.WatchDiscovery()
	if s.Lifetime != 1 || s.CatalogUsable {
		t.Fatalf("begun=%+v", s)
	}
	assertOpen(changed)
	ctx = e.discoveryReconciling(ctx, DiscoveryCacheWriter)
	assertClosed(changed)
	s, changed = e.WatchDiscovery()
	if !s.CatalogUsable || s.Complete {
		t.Fatalf("ready=%+v", s)
	}
	e.stopDiscovery()
	assertClosed(changed)
	s, changed = e.WatchDiscovery()
	if !s.Complete || s.State != DiscoveryFailed {
		t.Fatalf("stopped=%+v", s)
	}
	old := ctx
	ctx = e.beginDiscovery(context.Background())
	assertClosed(changed)
	s, changed = e.WatchDiscovery()
	if s.Lifetime != 2 || s.CatalogUsable {
		t.Fatalf("restart=%+v", s)
	}
	e.discoveryReconciling(old, DiscoveryCacheWriter)
	assertOpen(changed)
	e.discoveryReconciling(ctx, DiscoveryCacheFallback)
	assertClosed(changed)
}
