package codediagnostics

import (
	"context"
	"fmt"
	"testing"
)

func TestNavigationGrantsExternalAnswersOnly(t *testing.T) {
	m, s, p := multiDocFixture(t)
	p.entries[1].Capabilities.Definition = true
	nav := &navDocs{MultiOwnedDocumentProvider: p}
	m.SetDocumentProvider(nav)
	ctx, c := context.Background(), s.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "x = 1\n", ""); err != nil {
		t.Fatal(err)
	}
	define := func(paths ...string) {
		t.Helper()
		nav.locations = []NavigationLocation{{Path: "source.go"}}
		for _, path := range paths {
			nav.locations = append(nav.locations, NavigationLocation{Path: path, External: true})
		}
		if _, err := m.Navigate(ctx, c, "d1", 1, NavigateDefinition, Position{}); err != nil {
			t.Fatal(err)
		}
	}
	const fmtSource = "/usr/local/go/src/fmt/print.go"
	if err := m.NavigationGrant(c, fmtSource); errCode(err) != "notFound" {
		t.Fatal("granted before any answer:", err)
	}
	define(fmtSource)
	if err := m.NavigationGrant(c, fmtSource); err != nil {
		t.Fatal(err)
	}
	for path, code := range map[string]string{
		"/usr/local/go/src/fmt/scan.go":       "notFound",        // a neighbour
		"source.go":                           "invalidArgument", // project files are read the usual way
		"/usr/local/go/src/fmt/../os/file.go": "invalidArgument",
		"":                                    "invalidArgument",
	} {
		if err := m.NavigationGrant(c, path); errCode(err) != code {
			t.Fatalf("%q: %v", path, err)
		}
	}
	other := c
	other.AttachmentID = "another"
	if err := m.NavigationGrant(other, fmtSource); errCode(err) != "staleContext" {
		t.Fatal(err)
	}

	// At most maxNavigationGrants; the oldest goes first.
	for i := range maxNavigationGrants {
		define(fmt.Sprintf("/mod/p%d.go", i))
	}
	if err := m.NavigationGrant(c, fmtSource); errCode(err) != "notFound" {
		t.Fatal("oldest grant kept:", err)
	}
	if err := m.NavigationGrant(c, "/mod/p0.go"); err != nil {
		t.Fatal(err)
	}
	define("/mod/p0.go") // already granted: nothing is dropped
	if err := m.NavigationGrant(c, "/mod/p1.go"); err != nil {
		t.Fatal(err)
	}

	// Revoking approval ends every grant (and the generation): approving
	// again does not bring them back.
	revoked, err := m.Revoke(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.NavigationGrant(c, "/mod/p1.go"); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
	c = revoked.Context
	if err := m.NavigationGrant(c, "/mod/p1.go"); errCode(err) != "approvalRequired" {
		t.Fatal(err)
	}
	if _, err := m.Approve(c, revoked.Proposal.Fingerprint, "session"); err != nil {
		t.Fatal(err)
	}
	if err := m.NavigationGrant(c, "/mod/p1.go"); errCode(err) != "notFound" {
		t.Fatal("grant survived revoke:", err)
	}
	if err := m.Detach(c); err != nil {
		t.Fatal(err)
	}
	if err := m.NavigationGrant(c, "/mod/p1.go"); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
}

// An answer that arrives after the attachment ended grants nothing.
func TestNavigationGrantNeedsTheAttachmentAfterTheAnswer(t *testing.T) {
	m, s, p := multiDocFixture(t)
	p.entries[1].Capabilities.Definition = true
	ctx, c := context.Background(), s.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "x = 1\n", ""); err != nil {
		t.Fatal(err)
	}
	m.SetDocumentProvider(&detachingNav{navDocs: navDocs{MultiOwnedDocumentProvider: p, locations: []NavigationLocation{{Path: "/mod/a.go", External: true}}}, m: m, c: c})
	if _, err := m.Navigate(ctx, c, "d1", 1, NavigateDefinition, Position{}); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
}

type detachingNav struct {
	navDocs
	m *Manager
	c Context
}

func (d *detachingNav) Navigate(ctx context.Context, req NavigationRequest) (NavigationResult, error) {
	out, err := d.navDocs.Navigate(ctx, req)
	_ = d.m.Detach(d.c)
	return out, err
}
