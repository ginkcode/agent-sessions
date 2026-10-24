package scan

import (
	"context"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// DiscoveryObserver receives provider outcomes from an existing discovery pass.
// It performs no extra Detect or Scan calls and must be concurrency-safe.
type DiscoveryObserver interface {
	Detected(id model.AgentID, present bool, err error)
	Scanned(id model.AgentID, err error)
}

type discoveryObserverKey struct{}

// WithDiscoveryObserver attaches o to one discovery pass's context.
func WithDiscoveryObserver(ctx context.Context, o DiscoveryObserver) context.Context {
	return context.WithValue(ctx, discoveryObserverKey{}, o)
}

// ObserveDetection records one provider's detection result, if observed.
func ObserveDetection(ctx context.Context, id model.AgentID, present bool, err error) {
	if o, ok := ctx.Value(discoveryObserverKey{}).(DiscoveryObserver); ok {
		o.Detected(id, present, err)
	}
}

// ObserveScan records one detected provider's scan result, if observed.
func ObserveScan(ctx context.Context, id model.AgentID, err error) {
	if o, ok := ctx.Value(discoveryObserverKey{}).(DiscoveryObserver); ok {
		o.Scanned(id, err)
	}
}
