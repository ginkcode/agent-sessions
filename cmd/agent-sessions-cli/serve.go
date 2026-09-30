package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func serveCmd(ctx context.Context, args []string, roots paths.Roots, stdout, stderr io.Writer, stdin io.Reader) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stdio := fs.Bool("stdio", false, "run over stdio using JSON-RPC 2.0")
	nonce := fs.String("nonce", "", "startup synchronization nonce")
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

	// 2. Acquire cache advisory lock
	lock, lockErr := rpc.AcquireCacheLock(roots.Cache)
	if lockErr != nil {
		if errors.Is(lockErr, rpc.ErrRemoteBusy) {
			_, _ = fmt.Fprintln(stderr, "agent-sessions-cli serve: remote host is busy (cache locked by another session)")
			server := rpc.NewServer(nil, stdin, stdout)
			server.SetInitError(rpc.ErrRemoteBusy)
			_ = server.Serve(ctx)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: acquire cache lock: %v\n", lockErr)
		return 1
	}
	defer func() {
		_ = lock.Unlock()
	}()

	// 3. Create engine with roots and emitter
	// The engine's goroutines emit from Start on, before the server exists.
	var server atomic.Pointer[rpc.Server]
	emitter := engine.EmitterFunc(func(name string, payload any) {
		if s := server.Load(); s != nil {
			_ = s.Notify(name, payload)
		}
	})

	eng := engine.NewEngine(
		engine.WithRoots(roots),
		engine.WithCacheDir(roots.Cache),
		engine.WithCacheEnabled(true),
		engine.WithEmitter(emitter),
	)

	if err := eng.Start(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: start engine: %v\n", err)
		return 1
	}
	defer func() {
		_ = eng.Close()
	}()

	// 4. Run RPC server over stdio
	srv := rpc.NewServer(eng, stdin, stdout)
	srv.SetCapabilities(rpc.Capabilities{
		Trash:  eng.TrashSupported(),
		Manage: true,
		Export: true,
		Import: true,
		Search: true,
	})
	server.Store(srv)
	if err := srv.Serve(ctx); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli serve: %v\n", err)
		return 1
	}

	return 0
}
