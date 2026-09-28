package provider_test

import (
	"fmt"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func TestSetGet(t *testing.T) {
	pa := providertest.NewFake(model.AgentClaude, "Claude")
	pb := providertest.NewFake(model.AgentOpenCode, "OpenCode")
	set := provider.Set{pa, pb}

	if got, ok := set.Get(model.AgentClaude); !ok || got != pa {
		t.Fatalf("Get(claude) = %v, %v; want %v, true", got, ok, pa)
	}
	if got, ok := set.Get(model.AgentOpenCode); !ok || got != pb {
		t.Fatalf("Get(opencode) = %v, %v; want %v, true", got, ok, pb)
	}
	if _, ok := set.Get(model.AgentCodex); ok {
		t.Fatal("Get(codex) unexpectedly succeeded on an empty set")
	}
}

func TestDiagnosticsCap(t *testing.T) {
	var d provider.Diagnostics
	for i := 0; i < 150; i++ {
		d.Warn(fmt.Sprintf("f%d.jsonl", i), i, "warning %d", i)
	}
	if got := len(d.Warnings); got != provider.WarnCap {
		t.Fatalf("len(Warnings) = %d; want %d", got, provider.WarnCap)
	}
	if d.Dropped != 50 {
		t.Fatalf("Dropped = %d; want 50", d.Dropped)
	}
	if d.Warnings[99].Msg != "warning 99" {
		t.Fatalf("last retained warning = %q", d.Warnings[99].Msg)
	}
}

func TestDiagnosticsMerge(t *testing.T) {
	var d provider.Diagnostics
	d.ParseErrors = 1
	d.Unknown("foo")
	d.Warn("a.jsonl", 1, "one")

	var o provider.Diagnostics
	o.ParseErrors = 2
	o.Unknown("foo")
	o.Unknown("bar")
	for i := 0; i < 120; i++ {
		o.Warn("b.jsonl", i, "b %d", i)
	}

	d.Merge(o)

	if d.ParseErrors != 3 {
		t.Fatalf("ParseErrors = %d; want 3", d.ParseErrors)
	}
	if d.UnknownTypes["foo"] != 2 || d.UnknownTypes["bar"] != 1 {
		t.Fatalf("UnknownTypes = %v", d.UnknownTypes)
	}
	if got := len(d.Warnings); got != provider.WarnCap {
		t.Fatalf("len(Warnings) = %d; want %d", got, provider.WarnCap)
	}
	// 1 existing + 99 fill the cap from o; the remaining 21 of o's 120 count as dropped.
	if d.Dropped != 21 {
		t.Fatalf("Dropped = %d; want 21", d.Dropped)
	}
}

func TestFakeImplementsProvider(t *testing.T) {
	f := providertest.NewFake(model.AgentCodex, "Fake Codex")
	if _, ok := any(f).(provider.Provider); !ok {
		t.Fatal("*providertest.Fake does not implement provider.Provider")
	}
	if _, ok := any(f).(provider.LiveDetector); !ok {
		t.Fatal("*providertest.Fake does not implement provider.LiveDetector")
	}
}
