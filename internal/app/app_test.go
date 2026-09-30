package app

import (
	"testing"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestPing(t *testing.T) {
	a := NewApp()
	if got := a.Ping(""); got != "Hello from agent-sessions backend!" {
		t.Errorf("Ping(\"\") = %q, want default greeting", got)
	}
	if got := a.Ping("dev"); got != "Hello dev, welcome to agent-sessions!" {
		t.Errorf("Ping(%q) = %q, want personalized greeting", "dev", got)
	}
}

func TestOnStartupStoresContext(t *testing.T) {
	// OnStartup is invoked by the Wails runtime before any bound call; the
	// service must retain the context for later runtime calls (clipboard,
	// events, ...). Verified here with a plain background context.
	a := NewApp()
	ctx := t.Context()
	a.OnStartup(ctx)
	if a.ctx != ctx {
		t.Error("OnStartup did not retain the runtime context")
	}
}

func TestOnStartupCacheFirstLoadsCatalog(t *testing.T) {
	// 1. Seed a private cache DB with one session.
	db, err := index.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seed := model.SessionMeta{
		Ref:   model.SessionRef{Agent: model.AgentClaude, ID: "cached1"},
		Title: "Cached Session",
	}
	if err := db.CommitScan(t.Context(), model.AgentClaude, provider.ScanResult{
		Changed: []model.SessionMeta{seed},
	}); err != nil {
		t.Fatalf("CommitScan: %v", err)
	}
	_ = db.Close()

	// 2. Build an App with a fake provider and the same cache dir.
	fake := providertest.NewFake(model.AgentClaude, "Claude")
	fake.DetectionData = provider.Detection{Present: true}
	fake.Sessions = []model.SessionMeta{{
		Ref:   model.SessionRef{Agent: model.AgentClaude, ID: "live1"},
		Title: "Live Session",
	}}

	catalog := scan.NewCatalog()
	svc := NewService(catalog, provider.Set{fake})
	a := NewAppWithService(svc)
	a.cacheEnabled = true
	a.cacheDirOverride = db.CacheDir()

	// Cache-first startup must synchronously populate the catalog.
	a.OnStartup(t.Context())

	if m, ok := a.svc.Catalog().Get(model.SessionRef{Agent: model.AgentClaude, ID: "cached1"}); !ok || m.Title != "Cached Session" {
		t.Errorf("expected cached1 in catalog after OnStartup, got %+v", m)
	}
	if a.refresher == nil {
		t.Fatal("expected refresher to be wired")
	}
	if a.refresher.Generation() != 1 {
		t.Errorf("expected generation 1, got %d", a.refresher.Generation())
	}
}
