package rpc

import (
	"context"
	"testing"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

func (b *diagnosticsBackend) NavigateCode(ctx context.Context, c cd.Context, id string, v int64, op string, pos cd.Position) (cd.NavigationResult, error) {
	b.navCalls++
	out, err := b.m.Navigate(ctx, c, id, v, op, pos)
	if err == nil && b.tamper != nil {
		b.tamper(&out)
	}
	return out, err
}

// navSession is a running session that answers navigation with a fixed list.
type navSession struct {
	*rpcSession
	answer []cd.NavigationLocation
	reqs   []cd.NavigationRequest
}

func (n *navSession) OwnerSession(root cd.AuthorizedRoot, path string) (cd.DocumentOwnerIdentity, string, cd.ServerCapabilities, error) {
	owner, gen, caps, err := n.rpcSession.OwnerSession(root, path)
	caps.Definition, caps.References, caps.Hover = true, true, true
	return owner, gen, caps, err
}

func (n *navSession) Navigate(_ context.Context, req cd.NavigationRequest) (cd.NavigationResult, error) {
	n.reqs = append(n.reqs, req)
	if req.Op == cd.NavigateHover {
		return cd.NavigationResult{Hover: &cd.HoverText{Kind: "markdown", Value: "```go\nfunc f()\n```"}}, nil
	}
	return cd.NavigationResult{Locations: n.answer}, nil
}

func navFixture(t *testing.T) (*diagnosticsBackend, *Client, cd.Context, *navSession, context.Context, func()) {
	t.Helper()
	b, c, token, sess, ctx, done := liveFixture(t, true)
	if !c.Capabilities().CodeNavigation {
		t.Fatal("codeNavigation not advertised")
	}
	nav := &navSession{rpcSession: sess}
	b.m.SetDocumentProvider(nav)
	if _, err := c.OpenCodeDiagnosticDocument(ctx, token, "doc.go", "d1", 1, "package x\n\nfunc f() {}\n", "r1"); err != nil {
		t.Fatal(err)
	}
	return b, c, token, nav, ctx, done
}

func TestNavigationOverRPC(t *testing.T) {
	b, c, token, nav, ctx, done := navFixture(t)
	defer done()
	nav.answer = []cd.NavigationLocation{
		{Path: "doc.go", Range: cd.Range{Start: cd.Position{Line: 2, Character: 5}, End: cd.Position{Line: 2, Character: 6}}},
		{Path: "/usr/lib/go/src/fmt/print.go", External: true},
	}
	at := cd.Position{Line: 2, Character: 5}
	got, err := c.NavigateCode(ctx, token, "d1", 1, cd.NavigateDefinition, at)
	if err != nil || got.Op != cd.NavigateDefinition || got.DocumentID != "d1" || got.Version != 1 || len(got.Locations) != 2 || !got.Locations[1].External {
		t.Fatal(got, err)
	}
	if req := nav.reqs[0]; req.Path != "doc.go" || req.Position != at || req.Op != cd.NavigateDefinition {
		t.Fatalf("%+v", req)
	}
	if got, err = c.NavigateCode(ctx, token, "d1", 1, cd.NavigateHover, at); err != nil || got.Hover == nil || got.Hover.Kind != "markdown" || got.Locations == nil {
		t.Fatal(got, err)
	}
	nav.answer = nil
	if got, err = c.NavigateCode(ctx, token, "d1", 1, cd.NavigateReferences, at); err != nil || got.Locations == nil || len(got.Locations) != 0 {
		t.Fatal(got, err)
	}

	// The manager's own refusals keep their codes across the wire.
	if _, err = c.NavigateCode(ctx, token, "d1", 2, cd.NavigateHover, at); rpcCode(err) != "staleVersion" {
		t.Fatal(err)
	}
	if _, err = c.NavigateCode(ctx, token, "d1", 1, cd.NavigateHover, cd.Position{Line: 40}); rpcCode(err) != "invalidArgument" {
		t.Fatal(err)
	}

	// A peer that bypasses the client: unknown operations, a missing position
	// and a foreign context never reach the backend.
	calls := b.navCalls
	var out cd.NavigationResult
	for _, p := range []codeDiagnosticsParams{
		{Context: token, DocumentID: "d1", Version: 1, Op: "rename", Position: &at},
		{Context: token, DocumentID: "d1", Version: 1, Op: cd.NavigateHover},
	} {
		if err = c.call(ctx, "navigateCode", p, &out); rpcCode(err) != "invalidArgument" {
			t.Fatal(p.Op, err)
		}
	}
	foreign := token
	foreign.ConnectionID = "other"
	if err = c.call(ctx, "navigateCode", codeDiagnosticsParams{Context: foreign, DocumentID: "d1", Version: 1, Op: cd.NavigateHover, Position: &at}, &out); rpcCode(err) != "staleContext" {
		t.Fatal(err)
	}
	if b.navCalls != calls {
		t.Fatal("refused request reached the backend")
	}
}

// An answer the client did not ask for, or one a local server could not have
// given, is refused.
func TestNavigationClientRefusesBadAnswers(t *testing.T) {
	b, c, token, nav, ctx, done := navFixture(t)
	defer done()
	nav.answer = []cd.NavigationLocation{{Path: "doc.go"}}
	at := cd.Position{Line: 2, Character: 5}
	for name, tc := range map[string]struct {
		tamper func(*cd.NavigationResult)
		code   string
	}{
		"other version":   {func(r *cd.NavigationResult) { r.Version = 7 }, "staleContext"},
		"other document":  {func(r *cd.NavigationResult) { r.DocumentID = "d2" }, "staleContext"},
		"other operation": {func(r *cd.NavigationResult) { r.Op = cd.NavigateReferences }, "staleContext"},
		"escaping path":   {func(r *cd.NavigationResult) { r.Locations[0].Path = "../secret" }, "toolFailed"},
		"relative outside": {func(r *cd.NavigationResult) {
			r.Locations[0] = cd.NavigationLocation{Path: "lib/../../x", External: true}
		}, "toolFailed"},
		"backwards range": {func(r *cd.NavigationResult) {
			r.Locations[0].Range = cd.Range{Start: cd.Position{Line: 3}, End: cd.Position{Line: 1}}
		}, "toolFailed"},
		"odd hover": {func(r *cd.NavigationResult) { r.Hover = &cd.HoverText{Kind: "html", Value: "<b>"} }, "toolFailed"},
	} {
		b.tamper = tc.tamper
		if _, err := c.NavigateCode(ctx, token, "d1", 1, cd.NavigateDefinition, at); rpcCode(err) != tc.code {
			t.Fatal(name, err)
		}
	}
	b.tamper = func(r *cd.NavigationResult) {
		r.Locations = []cd.NavigationLocation{{Path: `C:\Go\src\fmt\print.go`, External: true}}
	}
	if got, err := c.NavigateCode(ctx, token, "d1", 1, cd.NavigateDefinition, at); err != nil || got.Locations[0].Path != `C:\Go\src\fmt\print.go` {
		t.Fatal("a Windows host's path", got, err)
	}
}

// A server without navigation: the client does not ask, and a client that
// asks anyway is refused before the backend.
func TestNavigationUnsupportedFailsBeforeAnyWork(t *testing.T) {
	b, c, token, _, ctx, done := navFixture(t)
	defer done()
	c.mu.Lock()
	c.caps.CodeNavigation = false
	c.mu.Unlock()
	at := cd.Position{Line: 2, Character: 5}
	if _, err := c.NavigateCode(ctx, token, "d1", 1, cd.NavigateHover, at); rpcCode(err) != "unsupported" {
		t.Fatal(err)
	}
	// The server negotiated it, but here pretends it did not.
	b2, c2, token2, _, ctx2, done2 := liveFixture(t, false)
	defer done2()
	if c2.Capabilities().CodeNavigation {
		t.Fatal("navigation advertised without live documents")
	}
	var out cd.NavigationResult
	if err := c2.call(ctx2, "navigateCode", codeDiagnosticsParams{Context: token2, DocumentID: "d1", Version: 1, Op: cd.NavigateHover, Position: &at}, &out); rpcCode(err) != "unsupported" {
		t.Fatal(err)
	}
	if b.navCalls != 0 || b2.navCalls != 0 {
		t.Fatal("unsupported request reached the backend")
	}
}
