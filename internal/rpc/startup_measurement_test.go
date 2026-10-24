package rpc

import (
	"context"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// Generated in-process workload: fixed 150 ms unrelated startup work after
// publication. This is not a simulated SSH latency or a native-paint benchmark.
func TestRemoteStartupMeasurements(t *testing.T) {
	if os.Getenv("AS_STARTUP_BASELINE") != "1" {
		t.Skip("set AS_STARTUP_BASELINE=1")
	}
	for _, early := range []bool{false, true} {
		var first []time.Duration
		for range 9 {
			serverObs, clientObs := engine.NewStartupObservation(), engine.NewStartupObservation()
			serverCtx, cancel := context.WithCancel(engine.WithStartupObservation(t.Context(), serverObs))
			clientCtx := engine.WithStartupObservation(t.Context(), clientObs)
			inR, inW := io.Pipe()
			outR, outW := io.Pipe()
			s := NewServer(nil, inR, outW)
			c := NewClient(outR, inW)
			b := &startupBackend{mockBackend: &mockBackend{}}
			start := func(ctx context.Context) error {
				b.publish(engine.DiscoveryStatus{Lifetime: 1, CatalogUsable: true})
				timer := time.NewTimer(150 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if early {
				s.SetPreparer(func(context.Context) (PreparedBackend, error) { return PreparedBackend{Backend: b, Start: start}, nil })
			} else {
				s.SetStarter(func(ctx context.Context) (engine.Backend, Capabilities, paths.Roots, error) {
					return b, Capabilities{}, paths.Roots{}, start(ctx)
				})
			}
			done := make(chan error, 1)
			go func() { done <- s.Serve(serverCtx) }()
			begin := time.Now()
			_, err := c.Initialize(clientCtx, InitializeRequest{})
			if err == nil {
				_, err = c.ListSessions(clientCtx, "", engine.FilterOpts{}, engine.SortOpts{})
			}
			first = append(first, time.Since(begin))
			// Wait out synthetic work before cleanup so early measurements
			// include a successful full-start sample as well as early readiness.
			if err == nil {
				err = s.awaitStartup(clientCtx, "getSettings")
			}
			cancel()
			_ = c.Close()
			_ = s.Close()
			<-done
			_ = inW.Close()
			_ = outR.Close()
			if err != nil {
				t.Fatal(err)
			}
			if early {
				seen := map[string]bool{}
				for _, sample := range serverObs.Samples() {
					seen[sample.Phase] = true
				}
				for _, phase := range []string{"rpc_prepare", "rpc_diagnostics_identity", "rpc_catalog_ready_wait", "rpc_start"} {
					if !seen[phase] {
						t.Fatalf("missing %s", phase)
					}
				}
			}
			if len(clientObs.Samples()) != 1 || clientObs.Samples()[0].Phase != "client_sessions" {
				t.Fatalf("client samples=%+v", clientObs.Samples())
			}
		}
		slices.Sort(first)
		t.Logf("early=%v initialize->first successful catalog (empty fake response): median=%v max=%v trials=%d", early, first[4].Round(time.Microsecond), first[8].Round(time.Microsecond), len(first))
	}
}

func TestPreparedFailureAfterReadyAndGateCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	b := &startupBackend{mockBackend: &mockBackend{}}
	s, c, cleanup := setupPipePair(nil)
	defer cleanup()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.SetPreparer(func(context.Context) (PreparedBackend, error) {
		return PreparedBackend{Backend: b, Start: func(ctx context.Context) error {
			b.publish(engine.DiscoveryStatus{Lifetime: 1, CatalogUsable: true})
			select {
			case <-release:
				return io.ErrUnexpectedEOF
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, nil
	})
	if _, err := c.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	// Cancel over the actual RPC transport, while the verified owner is held.
	requestCtx, requestCancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := c.GetMessages(requestCtx, model.SessionRef{}, 0, 10); result <- err }()
	requestCancel()
	if err := <-result; err == nil {
		t.Fatal("canceled transcript request succeeded")
	}
	// Catalog still answers independently of the canceled gated request.
	if _, err := c.Ping(ctx, "alive"); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	select {
	case <-c.Done():
	case <-ctx.Done():
		t.Fatal("late startup failure did not disconnect")
	}
}
