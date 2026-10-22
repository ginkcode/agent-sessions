package codediagnostics

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompositeDiscoveryTrust(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, tools, _ := fixture(t)
	checker := &fakeChecker{tools: tools, calls: make(chan adapterCall, 1)}
	regs := []Registration{{ProviderID: "checker", Availability: "available", Adapter: checker}}
	for _, id := range []string{goplsProviderID, tsProviderID} {
		cfg := ServerConfig{ProviderID: id, Executable: tools[0].Executable, Args: []string{"--fixed"}, Env: []string{"HOME=" + root.Project().Root}, AdapterPolicy: "synthetic-v1", ConfigPaths: []string{"config"}}
		ext, lang := ".go", "go"
		if id == tsProviderID {
			ext, lang = ".ts", "typescript"
		}
		if err := os.WriteFile(filepath.Join(root.Project().Root, "source"+ext), []byte("aa"), 0o600); err != nil {
			t.Fatal(err)
		}
		regs = append(regs, liveRegistration(ProviderState{ProviderID: id, Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}}, cfg, func(file string) (string, bool) {
			return lang, strings.HasSuffix(file, ext)
		}))
	}
	regs = append(regs, liveRegistration(ProviderState{ProviderID: svelteLSProviderID, Availability: "missingTool", Coverage: Coverage{Kind: "openedDocuments"}}, ServerConfig{}, nil))
	m, err := NewManager("composite", e.trust, NewSupervisor(e.trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	s, err := m.Attach(root, 1, regs)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Proposal.Tools) != 3 {
		t.Fatal("proposal does not include checker and both live policies", s.Proposal)
	}
	docs, err := DiscoveryDocuments(root, regs, e.mgr)
	if err != nil {
		t.Fatal(err)
	}
	goServer, ok := docs.Resolve(root, "source.go")
	if !ok {
		t.Fatal("Go owner missing")
	}
	tsServer, ok := docs.Resolve(root, "source.ts")
	if !ok {
		t.Fatal("TS owner missing")
	}
	for _, cfg := range []ServerConfig{goServer.Config, tsServer.Config} {
		_, err := e.mgr.Attach(context.Background(), root, cfg)
		code(t, err, "approvalRequired")
	}
	if e.launches() != 0 {
		t.Fatal("started before approval")
	}
	// A single-server grant cannot stand in for the composite proposal.
	fp, err := Fingerprint(root, []ToolInput{goServer.Config.Tool()})
	if err != nil {
		t.Fatal(err)
	}
	approve(t, e.trust, root, []ToolInput{goServer.Config.Tool()}, fp, "session")
	_, err = e.mgr.Attach(context.Background(), root, goServer.Config)
	code(t, err, "approvalRequired")
	if e.launches() != 0 {
		t.Fatal("subset grant started composite")
	}
	s, err = m.Approve(s.Context, s.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []ServerConfig{goServer.Config, tsServer.Config} {
		if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if e.launches() != 2 {
		t.Fatal("providers did not start independently")
	}
	for _, cfg := range []ServerConfig{goServer.Config, tsServer.Config} {
		if _, err := e.mgr.Attach(context.Background(), root, cfg); err != nil {
			t.Fatal("composite reuse invalidated peer", err)
		}
	}
	if e.launches() != 2 {
		t.Fatal("reuse started duplicate sessions")
	}
	// Run-all remains checker-only; live metadata is not a CheckerAdapter.
	runs, err := m.Run(s.Context, nil, false)
	if err != nil || len(runs) != 1 || runs[0].ProviderID != "checker" {
		t.Fatal("run-all selected live providers", runs, err)
	}
	call := <-checker.calls
	call.result <- CheckerResult{}
	for _, p := range s.Providers {
		if p.ProviderID == svelteLSProviderID && p.Availability != "missingTool" {
			t.Fatal("missing provider hidden by approval", p)
		}
	}
	// Policy tampering must fail membership, even under a valid whole grant.
	changed := cloneServer(goServer.Config)
	changed.Args = []string{"--unapproved"}
	if _, err := e.mgr.Attach(context.Background(), root, changed); err == nil {
		t.Fatal("unapproved argv accepted")
	}
	changed = cloneServer(goServer.Config)
	changed.ConfigPaths = nil
	if _, err := e.mgr.Attach(context.Background(), root, changed); err == nil {
		t.Fatal("server config identity omitted from proposal")
	}
	if err := os.WriteFile(filepath.Join(root.Project().Root, "config"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = e.mgr.Attach(context.Background(), root, tsServer.Config)
	code(t, err, "approvalRequired")
	if e.launches() != 2 {
		t.Fatal("changed fingerprint launched")
	}
	// Current catalog permission is independent of the execution grant.
	denied := root
	denied.known = func(string) bool { return false }
	_, err = e.mgr.Attach(context.Background(), denied, goServer.Config)
	code(t, err, "unauthorized")
	if e.launches() != 2 {
		t.Fatal("unknown directory launched")
	}
	if _, ok := docs.Resolve(root, "../source.go"); ok {
		t.Fatal("resolver escaped root")
	}
	if !reflect.DeepEqual(goServer.Config.Tool(), regs[1].Live.Server.Tool()) {
		t.Fatal("binding changed provider policy identity")
	}
}

func TestDiscoveryResolverRejectsConflictingOwners(t *testing.T) {
	root, tools, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(root.Project().Root, "source.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var regs []Registration
	for _, id := range []string{"first", "second"} {
		regs = append(regs, liveRegistration(ProviderState{ProviderID: id, Availability: "available", Coverage: Coverage{Kind: "openedDocuments"}}, ServerConfig{ProviderID: id, Executable: tools[0].Executable, AdapterPolicy: "fixed"}, func(string) (string, bool) { return "go", true }))
	}
	docs, err := DiscoveryDocuments(root, regs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := docs.Resolve(root, "source.go"); ok {
		t.Fatal("conflicting owners selected by order")
	}
}

func TestLiveDiscoveryBoundedAndNoParentPromotion(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "go.mod"), []byte("module parent\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "nested")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := AuthorizeRoot("host", dir, func(p string) bool { return p == dir })
	if err != nil {
		t.Fatal(err)
	}
	env := LiveEnvironment{Go: GoplsEnvironment{Home: dir, Cache: dir, ModuleCache: dir, GoRoot: dir}, TS: TSEnvironment{Home: dir, NodeDir: dir}, Svelte: SvelteLSEnvironment{Home: dir, NodeDir: dir}}
	regs, err := DiscoverLive(root, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 3 || root.Project().Root == parent {
		t.Fatal("discovery promoted parent or lost providers")
	}
	for _, r := range regs {
		if r.Availability != "missingTool" && r.Availability != "unsupported" {
			t.Fatal("missing tool acquired a fallback", r)
		}
	}
	env.Path = make([]string, 65)
	_, err = DiscoverLive(root, env)
	code(t, err, "limitExceeded")
}
