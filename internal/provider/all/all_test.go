package all_test

import (
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
)

func TestProviders(t *testing.T) {
	roots := paths.Roots{
		Claude: "/tmp/fake-claude",
	}
	set := all.Providers(roots, nil)
	if len(set) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(set))
	}
	p, ok := set.Get(model.AgentClaude)
	if !ok || p == nil {
		t.Fatal("expected Claude provider in set")
	}
	if p.ID() != model.AgentClaude {
		t.Fatalf("expected ID %s, got %s", model.AgentClaude, p.ID())
	}
}
