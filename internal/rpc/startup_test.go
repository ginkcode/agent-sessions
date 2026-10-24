package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

type startupBackend struct {
	*mockBackend
	mu      sync.Mutex
	status  engine.DiscoveryStatus
	changed chan struct{}
}

func (b *startupBackend) WatchDiscovery() (engine.DiscoveryStatus, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	return b.status, b.changed
}

func (b *startupBackend) publish(s engine.DiscoveryStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = s
	if b.changed != nil {
		close(b.changed)
		b.changed = nil
	}
}

func (b *startupBackend) DiscoveryStatus(context.Context) (engine.DiscoveryStatus, error) {
	s, _ := b.WatchDiscovery()
	return s, nil
}

func TestPreparedStartupEarlyCatalogAndGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	b := &startupBackend{mockBackend: &mockBackend{}}
	s, c, cleanup := setupPipePair(nil)
	defer cleanup()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.SetPreparer(func(context.Context) (PreparedBackend, error) {
		return PreparedBackend{Backend: b, Start: func(ctx context.Context) error {
			b.publish(engine.DiscoveryStatus{Lifetime: 1, CatalogUsable: true, State: engine.DiscoveryReconciling})
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, nil
	})
	res, err := c.Initialize(ctx, InitializeRequest{})
	if err != nil || !res.Capabilities.DiscoveryStatus {
		t.Fatalf("initialize=%+v err=%v", res, err)
	}
	<-entered
	if _, err := c.Ping(ctx, "early"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListSessions(ctx, "", engine.FilterOpts{}, engine.SortOpts{}); err != nil {
		t.Fatal(err)
	}
	if status, err := c.DiscoveryStatus(ctx); err != nil || !status.CatalogUsable || status.Complete {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, err := c.Initialize(ctx, InitializeRequest{}); err == nil {
		t.Fatal("duplicate initialize succeeded")
	}
	// A canceled gate cannot enter the backend. Test the gate directly to
	// avoid scheduling-dependent claims about a request not yet dispatched.
	requestCtx, requestCancel := context.WithCancel(ctx)
	requestCancel()
	for _, method := range []string{"scan", "getMessages", "search", "indexProgress", "getSettings", "deleteSessions", "newUnknownMethod"} {
		if err := s.awaitStartup(requestCtx, method); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: %v", method, err)
		}
	}
	once.Do(func() { close(release) })
	if err := s.awaitStartup(ctx, "getSettings"); err != nil {
		t.Fatal(err)
	}
	if err := c.call(ctx, "newUnknownMethod", nil, nil); err == nil {
		t.Fatal("unknown method succeeded")
	}
	if s.backend != b {
		t.Fatal("backend wrapped")
	}
}

func TestPreparedStartupReadinessAndShutdown(t *testing.T) {
	for _, mode := range []string{"no-watcher", "stale", "failure", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			b := &startupBackend{mockBackend: &mockBackend{}, status: engine.DiscoveryStatus{Lifetime: 3, CatalogUsable: true}}
			s, c, cleanup := setupPipePair(nil)
			defer cleanup()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			s.SetPreparer(func(context.Context) (PreparedBackend, error) {
				backend := engine.Backend(b)
				if mode == "no-watcher" {
					backend = b.mockBackend
				}
				return PreparedBackend{Backend: backend, Start: func(ctx context.Context) error {
					close(entered)
					if mode == "failure" {
						return errors.New("generated startup failure")
					}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}}, nil
			})
			initialized := make(chan error, 1)
			go func() { _, err := c.Initialize(ctx, InitializeRequest{}); initialized <- err }()
			<-entered
			if mode == "failure" {
				if err := <-initialized; err == nil {
					t.Fatal("failed startup initialized")
				}
				return
			}
			select {
			case err := <-initialized:
				t.Fatalf("initialized prematurely: %v", err)
			default:
			}
			if mode == "shutdown" {
				_ = s.Close()
				if err := <-initialized; err == nil {
					t.Fatal("closed startup initialized")
				}
				return
			}
			once.Do(func() { close(release) })
			if err := <-initialized; err != nil {
				t.Fatal(err)
			}
		})
	}
}
