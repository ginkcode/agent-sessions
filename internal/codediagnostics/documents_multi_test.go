package codediagnostics

import (
	"context"
	"errors"
	"testing"
)

type multiDocs struct {
	*fakeDocs
	entries  []DocumentSession
	failed   string
	isolated map[string]bool
}

func (f *multiDocs) OwnerSessions(AuthorizedRoot, string) []DocumentSession { return f.entries }
func (f *multiDocs) Apply(ctx context.Context, step DocumentStep) error {
	if step.Owner.ProviderID == f.failed && step.Kind != "close" {
		return errors.New("send failed")
	}
	return f.fakeDocs.Apply(ctx, step)
}
func (f *multiDocs) PublicationIsolatedFor(_ AuthorizedRoot, _ string, id, _ string) bool {
	return f.isolated[id]
}

func multiDocFixture(t *testing.T) (*Manager, Snapshot, *multiDocs) {
	t.Helper()
	m, s, f := docFixture(t, incrementalCaps)
	p := &multiDocs{fakeDocs: f, isolated: map[string]bool{"ruff": true, "pyright": true}}
	for _, id := range []string{"ruff", "pyright"} {
		p.entries = append(p.entries, DocumentSession{Owner: DocumentOwnerIdentity{id, "policy-" + id}, Generation: "g-" + id, Capabilities: incrementalCaps})
	}
	m.SetDocumentProvider(p)
	return m, s, p
}

func TestDocumentMultipleProvidersIndependentLifecycle(t *testing.T) {
	m, s, p := multiDocFixture(t)
	ctx := context.Background()
	c := s.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "shared", 1, "x", ""); err != nil {
		t.Fatal(err)
	}
	if len(p.steps) != 2 || p.steps[0].Owner == p.steps[1].Owner {
		t.Fatalf("opens: %+v", p.steps)
	}
	publish := func(id, gen string, v int64) error {
		return m.PublishPathDiagnostics(m.attachments[s.Context.AttachmentID].root.Project(), id, gen, "source.go", &v, []Diagnostic{{Path: "source.go", Source: id, Message: id, Severity: "error"}})
	}
	if err := publish("ruff", "g-ruff", 1); err != nil {
		t.Fatal(err)
	}
	if err := publish("pyright", "g-pyright", 1); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetSnapshot(c)
	if len(got.ResultSets) != 2 {
		t.Fatalf("sets: %+v", got.ResultSets)
	}
	p.entries[0].Generation = "ruff-restarted"
	if err := publish("ruff", "g-ruff", 1); errCode(err) != "staleContext" {
		t.Fatalf("stale: %v", err)
	}
	if _, err := m.ChangeDocument(ctx, c, "shared", 2, "xy"); err != nil {
		t.Fatal(err)
	}
	if p.steps[2].Kind != "open" || p.steps[2].Generation != "ruff-restarted" || p.steps[3].Kind != "change" {
		t.Fatalf("restart: %+v", p.steps)
	}
	p.isolated["ruff"] = false
	if err := publish("ruff", "ruff-restarted", 2); errCode(err) != "resyncRequired" {
		t.Fatalf("isolation: %v", err)
	}
	if err := publish("pyright", "g-pyright", 2); err != nil {
		t.Fatal(err)
	}
	p.failed = "ruff"
	if _, err := m.ChangeDocument(ctx, c, "shared", 3, "xyz"); err != nil {
		t.Fatal(err)
	}
	p.isolated["ruff"] = true
	if err := publish("ruff", "ruff-restarted", 3); errCode(err) != "staleContext" {
		t.Fatalf("failed peer: %v", err)
	}
	if err := publish("pyright", "g-pyright", 3); err != nil {
		t.Fatal(err)
	}
	got, _ = m.GetSnapshot(c)
	if len(got.ResultSets) != 1 || got.ResultSets[0].ProviderID != "pyright" {
		t.Fatalf("failed peer retained current results: %+v", got.ResultSets)
	}
	if _, err := m.SaveDocument(ctx, c, "shared", 3, "saved"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CloseDocument(ctx, c, "shared"); err != nil {
		t.Fatal(err)
	}
	n := len(p.steps)
	if p.steps[n-1].Kind != "close" || p.steps[n-2].Kind != "close" {
		t.Fatalf("close: %+v", p.steps)
	}
	got, _ = m.GetSnapshot(c)
	if len(got.ResultSets) != 0 {
		t.Fatal("close retained results")
	}
}

func TestDocumentMultipleProvidersOpenFailureDoesNotDisablePeer(t *testing.T) {
	m, s, p := multiDocFixture(t)
	p.failed = "ruff"
	if _, err := m.OpenDocument(context.Background(), s.Context, "source.go", "shared", 1, "x", ""); err != nil {
		t.Fatal(err)
	}
	v := int64(1)
	if err := m.PublishPathDiagnostics(m.attachments[s.Context.AttachmentID].root.Project(), "ruff", "g-ruff", "source.go", &v, nil); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
	if err := m.PublishPathDiagnostics(m.attachments[s.Context.AttachmentID].root.Project(), "pyright", "g-pyright", "source.go", &v, nil); err != nil {
		t.Fatal(err)
	}
}
