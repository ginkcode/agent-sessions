package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// serveCmd runs the JSON-RPC server. resolveRoots runs during initialize,
// after the client's env overrides are applied, so the engine, its cache and
// the reported roots all use the overridden locations. Every client gets its
// own server process; they share the cache as index writer or readers (see
// internal/engine/cache.go), so none of them waits for another.
func serveCmd(ctx context.Context, args []string, resolveRoots func() (paths.Roots, error), stdout, stderr io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stdio := fs.Bool("stdio", false, "run over stdio using JSON-RPC 2.0")
	nonce := fs.String("nonce", "", "startup synchronization nonce")
	// The client heartbeats every 15s. Without this, a client that vanished
	// behind a half-open connection would keep the server, and possibly the
	// index writer role, alive until sshd notices, which by default takes
	// hours.
	idleTimeout := fs.Duration("idle-timeout", 60*time.Second, "exit after this long with no message from the client (0 disables)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*stdio {
		_, _ = fmt.Fprintln(stderr, "agent-sessions-cli serve: --stdio flag is required")
		return 2
	}

	// 1. Output preface if nonce provided
	if *nonce != "" {
		if err := rpc.WritePreface(stdout, *nonce); err != nil {
			_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: write preface: %v\n", err)
			return 1
		}
	}

	// The engine's goroutines emit from Start on, while initialize is still
	// running, so the server is published before the starter runs.
	var server atomic.Pointer[rpc.Server]
	emitter := engine.EmitterFunc(func(name string, payload any) {
		if s := server.Load(); s != nil {
			_ = s.Notify(name, payload)
		}
	})

	// Set by the starter. Serve waits for its handlers before returning, so
	// these are safe to read afterwards.
	var (
		eng     *engine.Engine
		initErr error
	)
	defer func() {
		if eng != nil {
			_ = eng.Close()
		}
	}()

	srv := rpc.NewServer(nil, stdin, stdout)
	srv.SetIdleTimeout(*idleTimeout)
	srv.SetStarter(func(sctx context.Context) (engine.Backend, rpc.Capabilities, paths.Roots, error) {
		roots, err := resolveRoots()
		if err != nil {
			initErr = fmt.Errorf("resolve roots: %w", err)
			return nil, rpc.Capabilities{}, paths.Roots{}, initErr
		}
		// 2. Create and start the engine
		e := engine.NewEngine(
			engine.WithRoots(roots),
			engine.WithCacheDir(roots.Cache),
			engine.WithCacheEnabled(true),
			engine.WithEmitter(emitter),
		)
		if err := e.Start(sctx); err != nil {
			initErr = fmt.Errorf("start engine: %w", err)
			return nil, rpc.Capabilities{}, paths.Roots{}, initErr
		}
		eng = e
		return e, rpc.Capabilities{
			Trash:  e.TrashSupported(),
			Manage: true,
			Export: true,
			Import: true,
			Search: true,
		}, roots, nil
	})
	server.Store(srv)

	// 3. Run RPC server over stdio
	err := srv.Serve(ctx)
	if initErr != nil {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: %v\n", initErr)
		return 1
	}
	if errors.Is(err, rpc.ErrIdleTimeout) {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: no message from the client for %v; exiting\n", *idleTimeout)
		return 1
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: %v\n", err)
		return 1
	}

	return 0
}
