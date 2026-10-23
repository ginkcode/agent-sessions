package codediagnostics

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDiscoverLocalLiveBindsToolchainAndHoldsOtherLanguages(t *testing.T) {
	skipUnlessProduction(t)
	root := goplsRoot(t, map[string]string{"go.mod": "module example.test/a\n\ngo 1.26\n", "a.go": "package a", "b.ts": "", "x/c.mjs": "", "c.svelte": ""})
	bin, env := goplsHost(t, "v0.24.0", goplsModulePath)
	if err := os.WriteFile(filepath.Join(env.GoRoot, "VERSION"), []byte("go1.26.8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tsPath, tsEnv := tsHost(t)
	live := LocalLiveEnvironment{GoPath: []string{bin}, Path: tsPath, Go: env, TS: tsEnv, Ruff: RuffEnvironment{Home: tsEnv.Home}, Pyright: PyrightEnvironment{Home: tsEnv.Home, NodeDir: tsEnv.NodeDir}}
	regs, err := DiscoverLocalLive(root, live)
	if err != nil || len(regs) != 5 {
		t.Fatal(err, regs)
	}
	g := regs[0]
	if g.ProviderID != goplsProviderID || g.Availability != "available" || g.Live == nil {
		t.Fatalf("%+v", g)
	}
	tools := g.Live.Server.Tools()
	ids := []string{}
	for _, tool := range tools {
		ids = append(ids, tool.ProviderID)
	}
	if !slices.Equal(ids, []string{goplsProviderID, "gopls-toolchain-bin-go", "gopls-toolchain-VERSION"}) {
		t.Fatal("toolchain not bound", ids)
	}
	// TypeScript sources make vtsls available without a package.json. No
	// Python file: Ruff stays a muted unsupported, and Svelte is held.
	if regs[1].ProviderID != tsProviderID || regs[1].Availability != "available" {
		t.Fatalf("%+v", regs[1])
	}
	for _, r := range regs[2:] {
		if r.Availability != "unsupported" || r.Live == nil || r.Live.State.Coverage.Kind != "openedDocuments" || (r.ProviderID == svelteLSProviderID && r.Live.Resolve != nil) {
			t.Fatalf("%+v", r)
		}
	}
	docs, err := DiscoveryDocuments(root, regs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The proposal binds both servers and their toolchains; Svelte is held.
	for file, want := range map[string]string{"a.go": goplsProviderID, "b.ts": tsProviderID, "x/c.mjs": tsProviderID, "c.svelte": ""} {
		server, ok := docs.Resolve(root, file)
		if ok != (want != "") || (ok && (server.Config.ProviderID != want || len(server.Config.ApprovalTools) != 10)) {
			t.Fatalf("%s: %v %+v", file, ok, server)
		}
	}
	// Changing toolchain metadata changes the composite fingerprint.
	before, err := Fingerprint(root, tools)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.GoRoot, "VERSION"), []byte("go1.99.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(root, tools)
	if err != nil || after == before {
		t.Fatal("toolchain change kept the fingerprint", err)
	}
	// No toolchain at all is an independent status, never an error.
	live.Go.GoRoot = ""
	regs, err = DiscoverLocalLive(root, live)
	if err != nil || regs[0].Availability != "missingTool" {
		t.Fatal(err, regs[0].Availability)
	}
	for _, over := range []LocalLiveEnvironment{{GoPath: make([]string, 65), Go: env, TS: tsEnv}, {Path: make([]string, 65), Go: env, TS: tsEnv}} {
		if _, err := DiscoverLocalLive(root, over); errCode(err) != "limitExceeded" {
			t.Fatal(err)
		}
	}
}
