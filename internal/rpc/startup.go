package rpc

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// PreparedBackend separates capability preparation from engine startup. The
// backend remains unwrapped so its optional interfaces retain their identity.
// Start, when nonnil, is owned and joined by Serve before it returns.
type PreparedBackend struct {
	Backend      engine.Backend
	Capabilities Capabilities
	Roots        paths.Roots
	Start        func(context.Context) error
}

type Preparer func(context.Context) (PreparedBackend, error)

// SetPreparer opts into early catalog readiness; SetStarter remains synchronous.
func (s *Server) SetPreparer(fn Preparer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preparer = fn
	s.starter = nil
}

type startupTask struct {
	done chan struct{}
	err  error // published by closing done
}

func (s *Server) startPrepared(ctx context.Context, start func(context.Context) error) (err error) {
	readyDone := engine.BeginStartupPhase(ctx, "rpc_catalog_ready_wait")
	defer func() { readyDone(err == nil) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	watcher, observable := s.backend.(engine.DiscoveryWatcher)
	var baseline uint64
	if observable {
		status, _ := watcher.WatchDiscovery()
		baseline = status.Lifetime
	}
	task := &startupTask{done: make(chan struct{})}
	s.mu.Lock()
	s.startup = task
	s.mu.Unlock()
	// Registered by an active initialize handler. Serve waits for handlers
	// before waiting for startups, so Add cannot race the shutdown Wait.
	s.startups.Add(1)
	go func() {
		defer s.startups.Done()
		startDone := engine.BeginStartupPhase(ctx, "rpc_start")
		task.err = start(ctx)
		startDone(task.err == nil)
		close(task.done)
		if task.err != nil {
			_ = s.Close()
		}
	}()
	for {
		if observable {
			status, changed := watcher.WatchDiscovery()
			if status.Lifetime > baseline && status.CatalogUsable {
				// Prefer an already known startup failure over provisional data.
				select {
				case <-task.done:
					return task.err
				default:
					return ctx.Err()
				}
			}
			select {
			case <-changed:
				continue
			case <-task.done:
				return task.err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case <-task.done:
			return task.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// awaitStartup protects provisional catalog rows from operations that require a
// verified engine. New methods wait by default rather than bypassing admission.
func (s *Server) awaitStartup(ctx context.Context, method string) error {
	switch method {
	case "ping", "listGroups", "listSessions", "agentCounts", "getSessionMeta", "discoveryStatus":
		return ctx.Err()
	}
	s.mu.Lock()
	task := s.startup
	s.mu.Unlock()
	if task == nil {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-task.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return task.err
	}
}
