package codediagnostics

import (
	"fmt"
	"testing"
)

func TestLiveBoundedSelectionOrderAndProviderTotal(t *testing.T) {
	limits := ManagerLimits{Findings: 3, FindingBytes: 1000}.defaults()
	var a, b boundedDiagnostics
	for i := 0; i < 100; i++ {
		d := Diagnostic{Path: "source.go", Message: fmt.Sprintf("%03d", i)}
		a.add(d, limits)
		d.Message = fmt.Sprintf("%03d", 99-i)
		b.add(d, limits)
	}
	if encodedSize(a.results()) != encodedSize(b.results()) || a.results()[0].Message != "000" || b.results()[2].Message != "002" {
		t.Fatal("not deterministic prefix")
	}
	m, s := liveBoundsFixture(t)
	attachment := m.attachments[s.Context.AttachmentID]
	for i := 30; i >= 0; i-- {
		id := fmt.Sprintf("provider%02d", i)
		attachment.snapshot.Providers = append(attachment.snapshot.Providers, ProviderState{ProviderID: id, Coverage: Coverage{Roots: []string{}}})
		set := ResultSet{ProviderID: id, Origin: "lsp", Scope: Scope{Kind: "document", Path: "source.go", DocumentID: id}, Diagnostics: make([]Diagnostic, 1000)}
		for j := range set.Diagnostics {
			set.Diagnostics[j] = Diagnostic{Path: "source.go", Message: fmt.Sprint(j), Source: id}
		}
		attachment.snapshot.ResultSets = append(attachment.snapshot.ResultSets, set)
	}
	m.commit(attachment)
	got := assertLiveBudgets(t, m, s.Context)
	n := 0
	for _, set := range got.ResultSets {
		n += len(set.Diagnostics)
	}
	if n != 10000 {
		t.Fatal(n)
	}
	if !provider(attachment, "provider30").Truncated {
		t.Fatal("missing provider status")
	}
}
