package codediagnostics

import "testing"

func TestReservedDocumentPublicationGuard(t *testing.T) {
	v := int64(3)
	d := DocumentIdentity{Context: Context{"connection", "attachment", "generation"}, Path: "source.go", DocumentID: "open-one", Version: v}
	basis := SavedBasis{Kind: "document", DocumentID: d.DocumentID, Version: &v}
	if !d.MatchesPublication(d.Context, d.Path, basis) {
		t.Fatal("matching version rejected")
	}
	for _, bad := range []SavedBasis{{Kind: "document", DocumentID: d.DocumentID}, {Kind: "document", DocumentID: "reopened", Version: &v}, {Kind: "saved", Version: &v}} {
		if d.MatchesPublication(d.Context, d.Path, bad) {
			t.Fatal("stale/uncertain basis accepted")
		}
	}
	old := int64(2)
	basis.Version = &old
	if d.MatchesPublication(d.Context, d.Path, basis) {
		t.Fatal("late version accepted")
	}
	basis.Version = &v
	c := d.Context
	c.Generation = "other"
	if d.MatchesPublication(c, d.Path, basis) || d.MatchesPublication(d.Context, "other.go", basis) {
		t.Fatal("wrong owner accepted")
	}
}
