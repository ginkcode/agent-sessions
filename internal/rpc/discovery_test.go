package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

var _ engine.DiscoveryBackend = (*Client)(nil)

func TestDiscoveryLocalRPCParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Generated fakes only: one detected provider and one isolated detect
	// failure make a non-trivial degraded snapshot through the real runner.
	good := providertest.NewFake(model.AgentClaude, "generated")
	good.DetectionData.Present = true
	bad := providertest.NewFake(model.AgentCodex, "generated")
	bad.DetectErr = errors.New("generated failure")
	root := t.TempDir()
	roots := paths.Roots{Claude: filepath.Join(root, "claude"), Config: filepath.Join(root, "config"), Cache: filepath.Join(root, "cache")}
	e := engine.NewEngine(engine.WithRoots(roots), engine.WithCacheEnabled(false), engine.WithService(engine.NewService(scan.NewCatalog(), provider.Set{good, bad})))
	defer func() { _ = e.Close() }()
	s, c, cleanup := setupPipePair(e)
	defer cleanup()
	// Use a starter with explicit roots, never the default-root initialize path.
	s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
		return e, Capabilities{}, paths.Roots{}, nil
	})
	res, err := c.Initialize(ctx, InitializeRequest{})
	if err != nil || !res.Capabilities.DiscoveryStatus {
		t.Fatalf("initialize = %+v, %v", res, err)
	}
	events := make(chan engine.DiscoveryStatus, 10)
	c.SetEmitter(engine.EmitterFunc(func(name string, payload any) {
		if name == engine.DiscoveryStatusEvent {
			events <- payload.(engine.DiscoveryStatus)
		}
	}))
	e.SetEmitter(engine.EmitterFunc(func(name string, payload any) {
		if err := s.Notify(name, payload); err != nil {
			t.Errorf("notify: %v", err)
		}
	}))
	for lifetime := uint64(1); lifetime <= 2; lifetime++ {
		if err := e.Start(ctx); err != nil {
			t.Fatal(err)
		}
		var last engine.DiscoveryStatus
		for !last.Complete {
			select {
			case ev := <-events:
				if ev.Lifetime != lifetime || ev.Revision <= last.Revision {
					t.Fatalf("event ordering: last=%+v event=%+v", last, ev)
				}
				last = ev
			case <-ctx.Done():
				t.Fatal("event barrier: ", ctx.Err())
			}
		}
		local, err := e.DiscoveryStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		remote, err := c.DiscoveryStatus(ctx)
		if err != nil || remote != local || last != local {
			t.Fatalf("local=%+v remote=%+v event=%+v err=%v", local, remote, last, err)
		}
		if last.State != engine.DiscoveryDegraded || last.DetectedProviders != 1 || last.FailedProviders != 1 || !last.CatalogUsable || last.CacheMode != engine.DiscoveryCacheDisabled {
			t.Fatalf("unexpected outcome %+v", last)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoveryOldHostFallback(t *testing.T) {
	for _, mode := range []string{"no_capability", "method_not_found", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			old := &mockBackend{}
			backend := engine.Backend(old)
			if mode == "unsupported" {
				backend = &unsupportedDiscovery{mockBackend: old}
			}
			s, c, cleanup := setupPipePair(backend)
			defer cleanup()
			s.SetStarter(func(context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
				return backend, Capabilities{}, paths.Roots{}, nil
			})
			res, err := c.Initialize(ctx, InitializeRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "unsupported" && res.Capabilities.DiscoveryStatus {
				t.Fatal("old host advertised discovery")
			}
			if mode == "method_not_found" {
				c.mu.Lock()
				c.caps.DiscoveryStatus = true
				c.mu.Unlock()
			}
			if _, err := c.DiscoveryStatus(ctx); !errors.Is(err, engine.ErrUnsupportedAction) {
				t.Fatalf("fallback = %v", err)
			}
			if pong, err := c.Ping(ctx, "ok"); err != nil || pong != "pong ok" {
				t.Fatalf("old browsing: %q, %v", pong, err)
			}
		})
	}
	b, err := json.Marshal(Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["discoveryStatus"]; present {
		t.Fatal("absent capability must be omitted")
	}
	// No capability means no request, even without a transport.
	if _, err := (&Client{}).DiscoveryStatus(t.Context()); !errors.Is(err, engine.ErrUnsupportedAction) {
		t.Fatal(err)
	}
}

type unsupportedDiscovery struct{ *mockBackend }

func (*unsupportedDiscovery) DiscoveryStatus(context.Context) (engine.DiscoveryStatus, error) {
	return engine.DiscoveryStatus{}, engine.ErrUnsupportedAction
}
