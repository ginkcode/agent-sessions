package codediagnostics

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// R15 cleanup accounting. Every assertion reads counters owned by the object
// under test (SessionManager.OwnedResources, LSPTransport.LiveGoroutines,
// Manager.OwnedWorkers) and the fake process/clock created by the test. Nothing
// matches host processes or counts runtime goroutines globally.

func (e *sessionEnv) transportOf(root AuthorizedRoot, cfg ServerConfig) *LSPTransport {
	e.mgr.mu.Lock()
	defer e.mgr.mu.Unlock()
	if en := e.mgr.entries[keyOf(root, cfg)]; en != nil && en.session != nil {
		return en.session.transport()
	}
	return nil
}

// assertOwnedClean checks that the manager's goroutines have finished. The last
// decrement happens in a deferred func after the goroutine's wake-up, so wait
// (bounded, no fixed sleep) for the counters rather than sampling once.
func assertOwnedClean(t *testing.T, what string, m *SessionManager, trs []*LSPTransport, procs []*fakeSrv) {
	t.Helper()
	eventually(t, what+": owned goroutines", func() bool {
		if m.OwnedResources() != (OwnedResources{}) {
			return false
		}
		for _, tr := range trs {
			if tr != nil && tr.LiveGoroutines() != 0 {
				return false
			}
		}
		return true
	})
	for i, p := range procs {
		if !closed(p.terminated) || !closed(p.exited) {
			t.Fatalf("%s: process %d not terminated/reaped", what, i)
		}
	}
}

func TestSessionCleanupRepeatedLifecycles(t *testing.T) {
	const cycles = 5
	modes := []string{"crash", "revoke", "disconnect", "idle", "retry-after-crash", "cancel-start"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t, SessionOptions{})
			var trs []*LSPTransport
			var procs []*fakeSrv
			for i := 0; i < cycles; i++ {
				root, cfg := e.project()
				if mode == "cancel-start" {
					e.script = func(f *fakeSrv) { f.read() } // never answers initialize
					ctx, cancel := context.WithCancel(context.Background())
					done := make(chan error, 1)
					go func() { _, err := e.mgr.Attach(ctx, root, cfg); done <- err }()
					procs = append(procs, e.next())
					cancel()
					<-done
					// The canceled caller leaves the session starting; the owner
					// (lease/stop) must still clean it up deterministically.
					if err := e.trust.Revoke(root); err != nil {
						t.Fatal(err)
					}
					eventually(t, "revoked start", func() bool { return e.mgr.Info(root, cfg).Phase != phaseStarting })
					trs = append(trs, e.transportOf(root, cfg))
					continue
				}
				e.script = serveInit(defaultInit)
				info, err := e.mgr.Attach(context.Background(), root, cfg)
				if err != nil {
					t.Fatal(err)
				}
				f := e.next()
				procs = append(procs, f)
				trs = append(trs, e.transportOf(root, cfg))
				switch mode {
				case "crash", "retry-after-crash":
					_ = f.Terminate() // process dies on its own
					eventually(t, "failed", func() bool { return e.mgr.Info(root, cfg).Phase == phaseFailed })
					if mode == "retry-after-crash" {
						if _, err := e.mgr.Retry(context.Background(), root, cfg); err != nil {
							t.Fatal(err)
						}
						f2 := e.next()
						procs = append(procs, f2)
						trs = append(trs, e.transportOf(root, cfg))
						e.mgr.Disconnect(root.Project().HostID)
					}
				case "revoke":
					if err := e.trust.Revoke(root); err != nil {
						t.Fatal(err)
					}
					eventually(t, "revoked", func() bool { return e.mgr.Info(root, cfg).Phase == phaseDisconnected })
				case "disconnect":
					e.mgr.Disconnect(root.Project().HostID)
				case "idle":
					e.mgr.Hide(root, cfg)
					eventually(t, "grace timer", func() bool { return e.clock.count(idleGrace) == i+1 })
					e.clock.Advance(idleGrace)
					eventually(t, "idle expired", func() bool { return e.mgr.Info(root, cfg).Phase == phaseStopped })
				}
				if e.mgr.CurrentGeneration(root, cfg, info.Generation) {
					t.Fatal("generation still current after teardown")
				}
				assertOwnedClean(t, mode, e.mgr, trs, procs)
			}
			e.mgr.Close()
			assertOwnedClean(t, mode+" close", e.mgr, trs, procs)
		})
	}
}

// The counters must actually observe live resources, or zero proves nothing.
func TestSessionCleanupCountersObserveLiveResources(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, cfg := e.project()
	if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
		t.Fatal(err)
	}
	f := e.next()
	tr := e.transportOf(root, cfg)
	if got := e.mgr.OwnedResources(); got.Runs != 1 || got.ProcessWaiters != 1 {
		t.Fatalf("live session not counted: %+v", got)
	}
	if tr.LiveGoroutines() != 3 {
		t.Fatalf("transport goroutines %d", tr.LiveGoroutines())
	}
	e.mgr.Hide(root, cfg)
	eventually(t, "grace waiter", func() bool { return e.mgr.OwnedResources().TimerWaiters >= 1 })
	e.mgr.Close()
	assertOwnedClean(t, "close", e.mgr, []*LSPTransport{tr}, []*fakeSrv{f})
}

func TestSessionCleanupRepeatedShutdownAndHideResume(t *testing.T) {
	for i := 0; i < 5; i++ {
		e := newEnv(t, SessionOptions{})
		var trs []*LSPTransport
		var procs []*fakeSrv
		for j := 0; j < 2; j++ {
			root, cfg := e.project()
			if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
				t.Fatal(err)
			}
			procs = append(procs, e.next())
			trs = append(trs, e.transportOf(root, cfg))
			// Hide then resume leaves one grace waiter that must exit on resume.
			e.mgr.Hide(root, cfg)
			eventually(t, "grace timer", func() bool { return e.clock.count(idleGrace) == j+1 })
			if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
				t.Fatal(err)
			}
		}
		e.mgr.Close()
		assertOwnedClean(t, "shutdown", e.mgr, trs, procs)
		if e.launches() != 2 {
			t.Fatal("hide/resume restarted a server")
		}
	}
}

func TestSessionCleanupHungShutdownAndLaunchRace(t *testing.T) {
	for i := 0; i < 3; i++ {
		e := newEnv(t, SessionOptions{})
		e.script = func(f *fakeSrv) {
			m, _ := f.read()
			id, _ := jsonID(m)
			f.send(`{"jsonrpc":"2.0","id":` + id + `,"result":` + defaultInit + `}`)
			for {
				if _, ok := f.read(); !ok {
					return
				}
			}
		}
		root, cfg := e.project()
		if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
			t.Fatal(err)
		}
		f := e.next()
		tr := e.transportOf(root, cfg)
		done := make(chan struct{})
		go func() { e.mgr.Close(); close(done) }()
		eventually(t, "shutdown timer", func() bool { return e.clock.count(shutdownTimeout) >= 1 })
		e.clock.Advance(shutdownTimeout)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("close hung")
		}
		assertOwnedClean(t, "hung shutdown", e.mgr, []*LSPTransport{tr}, []*fakeSrv{f})
	}
}

// Document-level owned workers: revoke/detach/close/crash with open documents.
func TestLiveDocumentsCleanupRepeated(t *testing.T) {
	for _, mode := range []string{"revoke", "detach", "close", "crash", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			for i := 0; i < 4; i++ {
				x := newLiveLifecycle(t)
				c := x.owner.snap.Context
				for _, d := range []struct{ path, id string }{{"a.go", "a"}, {"b.go", "b"}} {
					if _, err := x.owner.OpenDocument(context.Background(), c, d.path, d.id, 1, "text", ""); err != nil {
						t.Fatal(err)
					}
				}
				tr := x.transportOf(x.root, x.cfg)
				switch mode {
				case "revoke":
					if _, err := x.owner.Revoke(c); err != nil {
						t.Fatal(err)
					}
					x.mgr.Disconnect(x.root.Project().HostID)
				case "detach":
					if err := x.owner.Detach(c); err != nil {
						t.Fatal(err)
					}
					x.mgr.Disconnect(x.root.Project().HostID)
				case "close":
					x.owner.Close()
					x.mgr.Close()
				case "crash":
					_ = x.server.Terminate()
					eventually(t, "failed", func() bool { return x.mgr.Info(x.root, x.cfg).Phase == phaseFailed })
				case "disconnect":
					x.mgr.Disconnect(x.root.Project().HostID)
				}
				eventually(t, "document workers", func() bool { return x.owner.OwnedWorkers() == 0 })
				assertOwnedClean(t, mode, x.mgr, []*LSPTransport{tr}, []*fakeSrv{x.server})
				x.owner.Close()
				x.mgr.Close()
				if x.owner.OwnedWorkers() != 0 {
					t.Fatal("manager workers leaked after Close")
				}
				assertOwnedClean(t, mode+" final", x.mgr, []*LSPTransport{tr}, []*fakeSrv{x.server})
			}
		})
	}
}

func jsonID(m map[string]any) (string, bool) {
	v, ok := m["id"].(float64)
	if !ok {
		return "0", false
	}
	return strconv.FormatInt(int64(v), 10), true
}

// Transport owns three goroutines; each failure path must end all of them and
// fail pending waiters, repeatedly.
func TestTransportCleanupRepeatedCloseEOFAndPending(t *testing.T) {
	for _, mode := range []string{"close", "eof", "cancel-pending"} {
		for i := 0; i < 10; i++ {
			tr, srv := newFake(t, LSPLimits{})
			if tr.LiveGoroutines() != 3 {
				t.Fatalf("%s: %d goroutines", mode, tr.LiveGoroutines())
			}
			ctx, cancel := context.WithCancel(context.Background())
			errc := make(chan error, 1)
			go func() { _, err := tr.Call(ctx, "slow", nil); errc <- err }()
			srv.recv() // request reached the server and is pending
			switch mode {
			case "close":
				_ = tr.Close()
			case "eof":
				_ = srv.toTr.Close() // server closes stdout
				<-tr.Done()
				_ = tr.Close()
			case "cancel-pending":
				cancel()
			}
			if err := <-errc; err == nil {
				t.Fatalf("%s: pending call not failed", mode)
			}
			if mode == "cancel-pending" && tr.Status().Pending != 0 {
				t.Fatal("canceled call left a pending waiter")
			}
			_ = tr.Close()
			cancel()
			if tr.LiveGoroutines() != 0 || tr.Status().Pending != 0 {
				t.Fatalf("%s: leaked %d goroutines, %d pending", mode, tr.LiveGoroutines(), tr.Status().Pending)
			}
		}
	}
}
