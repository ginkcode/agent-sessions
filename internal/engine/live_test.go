package engine

import (
	"context"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// ctxLiveDetector fails like the Claude provider does when its context has
// ended.
type ctxLiveDetector map[string]provider.LiveInfo

func (d ctxLiveDetector) Live(ctx context.Context) (map[string]provider.LiveInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d, nil
}

// The manager is built once, during whichever request first needs it, and an
// RPC server cancels that request's context when it answers. Liveness must use
// each check's own context, or every later delete fails as unknown.
func TestClaudeLiveFuncUsesCallContext(t *testing.T) {
	live := claudeLiveFunc(ctxLiveDetector{"running": {PID: 1}})
	ctx := context.Background()

	for _, tc := range []struct {
		agent, id string
		want      bool
	}{
		{string(model.AgentClaude), "running", true},
		{string(model.AgentClaude), "running/agent-a1", true},
		{string(model.AgentClaude), "idle", false},
		{string(model.AgentCodex), "running", false},
	} {
		got, err := live(ctx, tc.agent, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("live(%s, %s) = %v, %v; want %v", tc.agent, tc.id, got, err, tc.want)
		}
	}

	ended, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := live(ended, string(model.AgentClaude), "idle"); err == nil {
		t.Error("a cancelled check reported liveness instead of failing")
	}
}
