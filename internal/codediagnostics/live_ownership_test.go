package codediagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type (
	legacyDocuments        struct{ DocumentProvider }
	changingOwnerDocuments struct {
		*fakeDocs
		owner DocumentOwnerIdentity
	}
)

func (p *changingOwnerDocuments) OwnerSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	gen, caps, err := p.Session(root, path)
	return p.owner, gen, caps, err
}

func TestDocumentOwnershipFailsClosed(t *testing.T) {
	m, s, f := docFixture(t, incrementalCaps)
	c := s.Context
	m.SetDocumentProvider(legacyDocuments{f})
	if _, err := m.OpenDocument(context.Background(), c, "source.go", "legacy", 1, "aa", ""); errCode(err) != "unsupported" {
		t.Fatal("generation-only provider accepted", err)
	}
	p := &changingOwnerDocuments{fakeDocs: f, owner: DocumentOwnerIdentity{goplsProviderID, "policy-a"}}
	m.SetDocumentProvider(p)
	if _, err := m.OpenDocument(context.Background(), c, "source.go", "owned", 1, "aa", ""); err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	if err := m.PublishPathDiagnostics(m.attachments[c.AttachmentID].root.Project(), "", "g1", "source.go", &one, nil); err == nil {
		t.Fatal("missing provider accepted")
	}
	p.owner.Policy = "policy-b"
	before, _ := m.GetSnapshot(c)
	if err := m.PublishDocumentResults(c, "owned", "g1", &one, nil); errCode(err) != "staleContext" {
		t.Fatal("policy change accepted", err)
	}
	after, _ := m.GetSnapshot(c)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("policy mismatch committed")
	}
	if _, err := m.ChangeDocument(context.Background(), c, "owned", 2, "bb"); errCode(err) != "resyncRequired" {
		t.Fatal("owner rebound", err)
	}
}

type splitOwnedSessions struct {
	first, second SessionDocuments
	firstID       string
}

func (s splitOwnedSessions) route(root AuthorizedRoot, path string) SessionDocuments {
	server, _ := s.first.Resolve(root, path)
	if server.Config.ProviderID == s.firstID {
		return s.first
	}
	return s.second
}

func (s splitOwnedSessions) Session(root AuthorizedRoot, path string) (string, ServerCapabilities, error) {
	return s.route(root, path).Session(root, path)
}

func (s splitOwnedSessions) OwnerSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	return s.route(root, path).OwnerSession(root, path)
}

func (s splitOwnedSessions) PublicationIsolated(root AuthorizedRoot, path, generation string) bool {
	return s.route(root, path).PublicationIsolated(root, path, generation)
}

func (s splitOwnedSessions) Apply(ctx context.Context, step DocumentStep) error {
	return s.route(step.Root, step.Path).Apply(ctx, step)
}

// O1–O3: two real adapter/session routes, one synchronization owner per file,
// and an overlapping saved checker. Only launch and server responses are fake.
func TestLiveConcreteOwnership(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "go-typescript"
		if mixed {
			name = "typescript-svelte"
		}
		t.Run(name, func(t *testing.T) {
			handled := make(chan struct{}, 16)
			var bridges []func(SessionNotification)
			notify := func(n SessionNotification) {
				for _, handle := range bridges {
					handle(n)
				}
				handled <- struct{}{}
			}
			e := newEnv(t, SessionOptions{Notifications: notify})
			e2 := newEnv(t, SessionOptions{Notifications: notify})
			got := &captured{}
			for _, env := range []*sessionEnv{e, e2} {
				env.script = func(f *fakeSrv) {
					for {
						frame, ok := f.read()
						if !ok {
							return
						}
						got.add(frame)
						id, _ := json.Marshal(frame["id"])
						switch frame["method"] {
						case "initialize":
							f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":` + defaultInit + `}`)
						case "shutdown":
							f.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":null}`)
						case "exit":
							_ = f.Terminate()
							return
						}
					}
				}
			}
			root, base := e.project()
			envs := []*sessionEnv{e, e2}
			ids := []string{goplsProviderID, tsProviderID}
			paths := []string{"a.go", "util.ts"}
			if mixed {
				ids = []string{tsProviderID, svelteLSProviderID}
				paths = []string{"util.ts", "App.svelte", "store.svelte.ts"}
			}
			cfgs := map[string]ServerConfig{}
			for i, id := range ids {
				cfg := base
				cfg.ProviderID = id
				cfgs[id] = cfg
				envs[i].approve(root, cfg)
			}
			for _, path := range paths {
				if err := os.WriteFile(filepath.Join(root.Project().Root, path), []byte("disk"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h := NewManagerForTest(t, root)
			bridges = []func(SessionNotification){NewGoplsBridge(root, h.Manager).Handle, NewTSBridge(root, h.Manager).Handle, NewSvelteBridge(root, h.Manager).Handle}
			ts := &TSPlan{root: root, availability: "available", config: cfgs[tsProviderID], packages: []string{""}}
			sv := &SvelteLSPlan{root: root, availability: "available", config: cfgs[svelteLSProviderID], packages: []string{""}}
			// Trust currently supports a single exact tool fingerprint per project.
			// Use independent approved session owners, not bypassed grants; R04/R05
			// must reconcile combined production approval/registration separately.
			docs := OwnedDocuments(e.mgr, ts, sv)
			docs2 := OwnedDocuments(e2.mgr, ts, sv)
			mixedResolve := docs.Resolve
			docs.Resolve = func(r AuthorizedRoot, path string) (DocumentServer, bool) {
				if !mixed && filepath.Ext(path) == ".go" {
					return DocumentServer{Config: cfgs[goplsProviderID], LanguageID: "go"}, true
				}
				return mixedResolve(r, path)
			}
			docs2.Resolve = docs.Resolve
			h.SetDocumentProvider(splitOwnedSessions{first: docs, second: docs2, firstID: ids[0]})
			servers := map[string]*fakeSrv{}
			generations := map[string]string{}
			for i, id := range ids {
				env := envs[i]
				info, err := env.mgr.Attach(context.Background(), root, cfgs[id])
				if err != nil {
					t.Fatal(err)
				}
				generations[id], servers[id] = info.Generation, env.next()
				again, err := env.mgr.Attach(context.Background(), root, cfgs[id])
				if err != nil || again.Generation != info.Generation {
					t.Fatal("session not reused", err)
				}
			}
			if e.launches()+e2.launches() != 2 {
				t.Fatal("duplicate package sessions")
			}
			c := h.snap.Context
			for _, path := range paths {
				if _, err := h.OpenDocument(context.Background(), c, path, path, 1, "aa", ""); err != nil {
					t.Fatal(err)
				}
			}
			// Notify delivery is not an acknowledgment; wait until the fake peers
			// have read all opens, then count exactly one stream for each URI.
			eventually(t, "one didOpen per file", func() bool {
				got.mu.Lock()
				defer got.mu.Unlock()
				n := 0
				for _, frame := range got.frames {
					if frame["method"] == "textDocument/didOpen" {
						n++
					}
				}
				return n == len(paths)
			})
			got.mu.Lock()
			seen := map[string]int{}
			for _, frame := range got.frames {
				if frame["method"] == "textDocument/didOpen" {
					p := frame["params"].(map[string]any)["textDocument"].(map[string]any)
					seen[p["uri"].(string)]++
				}
			}
			got.mu.Unlock()
			for _, path := range paths {
				if seen[uriOf(root, path)] != 1 {
					t.Fatal("duplicate/missing didOpen")
				}
			}
			// Use the checker replacement implementation, not a fabricated LSP owner.
			r := Range{Start: Position{0, 0}, End: Position{0, 1}}
			saved := Diagnostic{Path: paths[0], Range: &r, Severity: "error", Message: "same", Source: "saved/compiler", Basis: SavedBasis{Kind: "saved", Revision: "r1"}, Freshness: "current"}
			h.mu.Lock()
			h.apply(h.attachments[c.AttachmentID], "t", "run", CheckerResult{Replace: true, Phase: "idle", Diagnostics: []Diagnostic{saved}})
			h.commit(h.attachments[c.AttachmentID])
			h.mu.Unlock()
			owner := func(path string) string {
				if filepath.Ext(path) == ".go" {
					return goplsProviderID
				}
				id, _ := DocumentOwner(path)
				return id
			}
			snapshot := func() Snapshot {
				s, err := h.GetSnapshot(c)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			assertEvents := func() {
				s := snapshot()
				events, err := h.SnapshotEvents(c)
				if err != nil {
					t.Fatal(err)
				}
				var sets []ResultSet
				for _, event := range events {
					if event.Sequence != s.Sequence {
						t.Fatal("sequence mismatch")
					}
					sets = append(sets, event.Snapshot.ResultSets...)
				}
				if !reflect.DeepEqual(sets, s.ResultSets) {
					t.Fatal("snapshot event changed attribution")
				}
				updates, gap, err := h.DrainEvents(c)
				if err != nil || gap || len(updates) == 0 {
					t.Fatal("missing committed events", err)
				}
				sets = nil
				for _, event := range updates[len(updates)-1] {
					sets = append(sets, event.Snapshot.ResultSets...)
				}
				if !reflect.DeepEqual(sets, s.ResultSets) {
					t.Fatal("committed event changed attribution")
				}
			}
			// Drain setup before exercising each independently committed update.
			_, _, _ = h.DrainEvents(c)
			push := func(id, path string, empty bool) {
				ds := []any{}
				if !empty {
					ds = append(ds, liveDiagnostic("same", r))
				}
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": uriOf(root, path), "version": 1, "diagnostics": ds}})
				servers[id].send(string(raw))
				select {
				case <-handled:
				case <-time.After(10 * time.Second):
					t.Fatal("push barrier timed out")
				}
			}
			for round := 0; round < 2; round++ {
				for i := range paths {
					index := i
					if round == 1 {
						index = len(paths) - 1 - i
					}
					path := paths[index]
					push(owner(path), path, false)
					assertEvents()
				}
				s := snapshot()
				if len(s.ResultSets) != len(paths)+1 {
					t.Fatal("attribution deduplicated")
				}
				for _, set := range s.ResultSets {
					if set.Origin == "checker" {
						if set.ProviderID != "t" || !reflect.DeepEqual(set.Diagnostics, []Diagnostic{saved}) {
							t.Fatal("checker relabeled")
						}
						continue
					}
					if set.ProviderID != owner(set.Scope.Path) || set.Scope.Kind != "document" || set.Scope.DocumentID != set.Scope.Path {
						t.Fatalf("wrong owner: %+v", set)
					}
					d := set.Diagnostics[0]
					if d.Source != set.ProviderID+"/compiler" || d.Freshness != "current" || d.Basis.DocumentID != set.Scope.DocumentID || d.Basis.Version == nil || *d.Basis.Version != 1 || d.Range == nil || *d.Range != r {
						t.Fatalf("lost finding identity: %+v", d)
					}
				}
				// Exact equivalent saved/live findings remain independently attributed.
				if CountFindings(s).Errors != len(paths) {
					t.Fatal("display equivalence count changed")
				}
				target := paths[0]
				push(owner(target), target, true)
				after := snapshot()
				for i, set := range s.ResultSets {
					if set.Origin == "lsp" && set.Scope.Path == target {
						if len(after.ResultSets[i].Diagnostics) != 0 {
							t.Fatal("empty not applied")
						}
						continue
					}
					if !reflect.DeepEqual(set, after.ResultSets[i]) {
						t.Fatal("empty crossed ownership")
					}
				}
				assertEvents()
			}
			// Wrong-owner pushes (including empty) must not commit, even if the
			// caller borrows the target's generation and version. Sources are irrelevant.
			before := snapshot()
			one := int64(1)
			for _, target := range paths {
				wrong := ids[0]
				if wrong == owner(target) {
					wrong = ids[1]
				}
				for _, ds := range [][]Diagnostic{nil, {{Path: target, Message: "same", Source: owner(target), Severity: "error"}}} {
					if err := h.PublishPathDiagnostics(root.Project(), wrong, generations[owner(target)], target, &one, ds); errCode(err) != "staleContext" {
						t.Fatal("non-owner accepted", err)
					}
				}
				push(wrong, target, true)
				push(wrong, target, false)
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("non-owner changed snapshot/sequence")
			}
			// Simultaneous adapter callbacks serialize without lending attribution.
			var wg sync.WaitGroup
			for _, path := range paths {
				wg.Add(1)
				go func(path string) {
					defer wg.Done()
					raw, _ := json.Marshal(map[string]any{"uri": uriOf(root, path), "version": 1, "diagnostics": []any{liveDiagnostic("same", r)}})
					n := SessionNotification{Project: root.Project(), ProviderID: owner(path), Generation: generations[owner(path)], Method: "textDocument/publishDiagnostics", Params: raw}
					for _, handle := range bridges {
						handle(n)
					}
				}(path)
			}
			wg.Wait()
			for _, set := range snapshot().ResultSets {
				if set.Origin == "lsp" && set.ProviderID != owner(set.Scope.Path) {
					t.Fatal("concurrent relabel")
				}
			}
		})
	}
}
