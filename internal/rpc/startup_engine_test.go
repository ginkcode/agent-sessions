package rpc

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

type remoteStartupWatch struct {
	provider.Provider
	entered, release chan struct{}
	once             sync.Once
}

func (p *remoteStartupWatch) WatchPaths() []string {
	p.once.Do(func() { close(p.entered); <-p.release })
	return nil
}

func TestPreparedRealEngineCatalogDuringStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	roots := paths.Roots{Claude: filepath.Join(root, "generated"), Cache: filepath.Join(root, "cache"), Config: filepath.Join(root, "config")}
	db, err := index.Open(ctx, roots.Cache, index.RootsKey(roots.Claude, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	meta := model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "cached"}, CWD: "/generated/work"}
	if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{Changed: []model.SessionMeta{meta}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	fake := providertest.NewFake(model.AgentClaude, "generated")
	fake.DetectionData.Present = true
	watch := &remoteStartupWatch{Provider: fake, entered: make(chan struct{}), release: make(chan struct{})}
	e := engine.NewEngine(engine.WithRoots(roots), engine.WithService(engine.NewService(scan.NewCatalog(), provider.Set{watch})),
		engine.WithRemoteCodeDiagnostics("ssh"), engine.WithFixtureMachineIdentity("generated-machine"))
	defer func() { _ = e.Close() }()
	s, c, cleanup := setupPipePair(nil)
	defer cleanup()
	var once sync.Once
	defer once.Do(func() { close(watch.release) })
	s.SetPreparer(func(context.Context) (PreparedBackend, error) {
		return PreparedBackend{Backend: e, Roots: roots, Start: e.Start}, nil
	})
	res, err := c.Initialize(ctx, InitializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Capabilities.CodeDiagnostics || res.CodeDiagnosticsHostID == "" {
		t.Fatalf("remote identity/capability lost: %+v", res)
	}
	select {
	case <-watch.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	rows, err := c.ListSessions(ctx, "", engine.FilterOpts{}, engine.SortOpts{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, err := c.ListGroups(ctx, engine.GroupMode("dir-agent"), engine.FilterOpts{}); err != nil {
		t.Fatal(err)
	}
	if counts, err := c.AgentCounts(ctx, engine.FilterOpts{}); err != nil || counts[string(model.AgentClaude)] != 1 {
		t.Fatalf("counts=%v err=%v", counts, err)
	}
	if status, err := c.DiscoveryStatus(ctx); err != nil || !status.CatalogUsable || status.Complete {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	once.Do(func() { close(watch.release) })
	if err := s.awaitStartup(ctx, "getSettings"); err != nil {
		t.Fatal(err)
	}
}
