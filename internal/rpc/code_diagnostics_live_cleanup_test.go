package rpc

import (
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

// R15: repeated full-route cycles. Counters are owned by the Engine's
// diagnostics owner and every fake process was created by this fixture.
func (x *routeFixture) assertClean(what string) {
	x.t.Helper()
	eventually(x.t, what+": owned goroutines", func() bool {
		r, workers := x.engine.OwnedCodeDiagnosticsResources()
		return r == (cd.OwnedResources{}) && workers == 0
	})
	x.mu.Lock()
	all := append([]*routeServer(nil), x.all...)
	x.mu.Unlock()
	for i, f := range all {
		select {
		case <-f.exited:
		case <-time.After(10 * time.Second):
			x.t.Fatalf("%s: fake process %d not terminated", what, i)
		}
	}
}

func TestRouteRepeatedRevokeDetachAndCrashLeaveNothingOwned(t *testing.T) {
	for _, mode := range []string{"revoke", "detach", "crash", "engine-close", "rpc-disconnect"} {
		t.Run(mode, func(t *testing.T) {
			for i := 0; i < 3; i++ {
				x := newRouteFixture(t, routeOptions{})
				x.initialize(true)
				x.attachAndApprove(true)
				c := x.attach.Context
				for _, d := range []struct{ path, id string }{{"a.go", "da"}, {"b.ts", "db"}} {
					if _, err := x.client.OpenCodeDiagnosticDocument(x.ctx, c, d.path, d.id, 1, "text", "r1"); err != nil {
						t.Fatal(err)
					}
					x.next()
				}
				if r, _ := x.engine.OwnedCodeDiagnosticsResources(); r.Runs != 2 || r.ProcessWaiters != 2 {
					t.Fatalf("live sessions not counted: %+v", r)
				}
				switch mode {
				case "revoke":
					if _, err := x.client.RevokeCodeDiagnostics(x.ctx, c); err != nil {
						t.Fatal(err)
					}
				case "detach":
					if _, err := x.client.DetachCodeDiagnostics(x.ctx, c); err != nil {
						t.Fatal(err)
					}
				case "crash":
					x.mu.Lock()
					first := x.all[0]
					x.mu.Unlock()
					_ = first.Terminate()
					eventually(t, "one session gone", func() bool {
						r, _ := x.engine.OwnedCodeDiagnosticsResources()
						return r.Runs == 1
					})
				case "rpc-disconnect":
					// Client goes away: Serve ends and expires owned attachments.
					_ = x.client.Close()
					_ = x.server.Close()
					x.assertClean(mode)
				case "engine-close":
					if err := x.engine.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "revoke" {
					// Trust/lease cancellation ends sessions without waiting for idle grace.
					x.assertClean(mode)
				}
				if mode == "crash" {
					x.pump() // surfaces the failure through the event pump
				}
				_ = x.engine.Close()
				x.assertClean(mode + " close")
			}
		})
	}
}
