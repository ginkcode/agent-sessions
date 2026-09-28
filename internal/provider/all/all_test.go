package all_test

import (
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
)

func TestProviders(t *testing.T) {
	roots := paths.Roots{
		Claude:       "/tmp/fake-claude",
		Codex:        "/tmp/fake-codex",
		OpenCodeData: "/tmp/fake-opencode",
	}
	set := all.Providers(roots, nil)
	if len(set) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(set))
	}
	for _, id := range []model.AgentID{model.AgentClaude, model.AgentCodex, model.AgentOpenCode} {
		p, ok := set.Get(id)
		if !ok || p == nil {
			t.Fatalf("expected %s provider in set", id)
		}
	}
}
