package codediagnostics

import (
	"container/heap"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"
)

func diagnosticCompare(a, b Diagnostic) int {
	if n := strings.Compare(a.Path, b.Path); n != 0 {
		return n
	}
	if a.Range == nil && b.Range != nil {
		return -1
	}
	if a.Range != nil && b.Range == nil {
		return 1
	}
	if a.Range != nil {
		aa := []int{a.Range.Start.Line, a.Range.Start.Character, a.Range.End.Line, a.Range.End.Character}
		bb := []int{b.Range.Start.Line, b.Range.Start.Character, b.Range.End.Line, b.Range.End.Character}
		for i, n := range aa {
			if n < bb[i] {
				return -1
			}
			if n > bb[i] {
				return 1
			}
		}
	}
	for _, pair := range [][2]string{{a.Message, b.Message}, {a.Severity, b.Severity}, {a.Code, b.Code}, {a.Source, b.Source}, {a.Freshness, b.Freshness}} {
		if n := strings.Compare(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	// Stable tie-break for basis identity, never lend freshness across providers.
	aa, _ := json.Marshal(a.Basis)
	bb, _ := json.Marshal(b.Basis)
	return strings.Compare(string(aa), string(bb))
}

type diagnosticHeap []Diagnostic

func (h diagnosticHeap) Len() int           { return len(h) }
func (h diagnosticHeap) Less(i, j int) bool { return diagnosticCompare(h[i], h[j]) > 0 }
func (h diagnosticHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *diagnosticHeap) Push(v any)        { *h = append(*h, v.(Diagnostic)) }
func (h *diagnosticHeap) Pop() any {
	old := *h
	v := old[len(old)-1]
	old[len(old)-1] = Diagnostic{}
	*h = old[:len(old)-1]
	return v
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.Clone(s)
}

func validRange(r *Range) bool {
	if r == nil {
		return true
	}
	for _, n := range []int{r.Start.Line, r.Start.Character, r.End.Line, r.End.Character} {
		if n < 0 || int64(n) > maxSequence {
			return false
		}
	}
	return r.Start.Line < r.End.Line || (r.Start.Line == r.End.Line && r.Start.Character <= r.End.Character)
}

// The adapter owns text-coordinate proof. Manager validates shape/containment,
// and never upgrades uncertain evidence into a verified saved-file revision.
func normalizeDiagnostic(root AuthorizedRoot, d Diagnostic) (Diagnostic, bool, bool) {
	if _, err := root.ResolvePath(root.Project().HostID, d.Path); err != nil {
		return Diagnostic{}, false, true
	}
	if !utf8.ValidString(d.Message) || d.Message == "" || !bounded(d.Source, 256) || (d.Code != "" && !bounded(d.Code, 256)) || !slices.Contains([]string{"error", "warning", "information", "hint"}, d.Severity) || !slices.Contains([]string{"current", "uncertain", "stale"}, d.Freshness) || d.Basis.Kind != "saved" || d.Basis.DocumentID != "" || d.Basis.Version != nil || (d.Basis.Revision != "" && !bounded(d.Basis.Revision, 256)) {
		return Diagnostic{}, false, true
	}
	partial := len(d.Message) > 8192
	d.Path = strings.Clone(d.Path)
	d.Source = strings.Clone(d.Source)
	d.Code = strings.Clone(d.Code)
	d.Basis.Revision = strings.Clone(d.Basis.Revision)
	d.Message = strings.Clone(shorten(d.Message, 8192))
	if d.Freshness == "current" && d.Basis.Revision == "" {
		d.Freshness = "uncertain"
	}
	if !validRange(d.Range) || (d.LocationIssue != "" && d.LocationIssue != "invalidRange") {
		d.Range = nil
		d.LocationIssue = "invalidRange"
		partial = true
	}
	if d.Range != nil {
		r := *d.Range
		d.Range = &r
	}
	return d, true, partial
}

func (m *Manager) apply(a *attachment, id, runID string, r CheckerResult) {
	p := provider(a, id)
	if !r.Replace || r.Phase != "idle" || r.Failure != nil {
		phase := "failed"
		if r.Phase == "canceled" {
			phase = "canceled"
		}
		m.stale(a, id, phase)
		p.Truncated = p.Truncated || r.Truncated
		p.Failure = &Error{"toolFailed", "Checker did not complete; previous results retained stale"}
		if phase == "canceled" {
			p.Failure = &Error{"canceled", "Checker was canceled; previous results retained stale"}
		}
		if availability(r.Availability) {
			p.Availability = r.Availability
		}
		return
	}
	p.Phase = "idle"
	p.Failure = nil
	p.Truncated = r.Truncated
	p.Freshness = "uncertain" // saved checker evidence has no draft freshness proof
	p.Coverage = Coverage{Kind: "savedProject", Roots: []string{}, Partial: r.Partial || r.Truncated, ExternalChanges: "unwatched"}
	if utf8.ValidString(r.Notice) {
		p.Notice = strings.Clone(shorten(r.Notice, 2048))
	}
	for _, root := range r.Roots {
		if len(p.Coverage.Roots) >= 128 {
			p.Coverage.Partial = true
			break
		}
		if root != "" {
			if _, err := commandDirectory(a.root, root); err != nil {
				p.Coverage.Partial = true
				continue
			}
		}
		p.Coverage.Roots = append(p.Coverage.Roots, strings.Clone(root))
	}
	slices.Sort(p.Coverage.Roots)
	p.Coverage.Roots = slices.Compact(p.Coverage.Roots)
	// Bounded max heap selects a deterministic prefix without copying or retaining
	// an adapter's arbitrarily large candidate slice. Adapters also bound parsing.
	h := &diagnosticHeap{}
	for _, candidate := range r.Diagnostics {
		d, ok, partial := normalizeDiagnostic(a.root, candidate)
		if partial {
			p.Coverage.Partial = true
			p.Notice = "Some findings or locations were shortened or could not be verified; results are partial"
		}
		if !ok {
			p.Notice = OutsideRootNotice
			continue
		}
		if len(*h) < m.limits.Findings {
			heap.Push(h, d)
		} else {
			p.Truncated = true
			p.Coverage.Partial = true
			if diagnosticCompare(d, (*h)[0]) < 0 {
				(*h)[0] = d
				heap.Fix(h, 0)
			}
		}
	}
	diagnostics := []Diagnostic(*h)
	slices.SortFunc(diagnostics, diagnosticCompare)
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	}
	s := ResultSet{ProviderID: id, Origin: "checker", Scope: Scope{Kind: "project"}, RunID: runID, Diagnostics: diagnostics}
	found := false
	for i, old := range a.snapshot.ResultSets {
		if old.ProviderID == id && old.Origin == "checker" {
			a.snapshot.ResultSets[i] = s
			found = true
			break
		}
	}
	if !found {
		a.snapshot.ResultSets = append(a.snapshot.ResultSets, s)
	}
	slices.SortFunc(a.snapshot.ResultSets, func(a, b ResultSet) int { return strings.Compare(a.ProviderID, b.ProviderID) })
	m.retain(a)
}

func (m *Manager) retain(a *attachment) {
	count, bytes := 0, 0
	// The count/encoded budgets are attachment-wide, not per provider. Metadata is
	// separately bounded on attach and by normalized run coverage/notices.
	slices.SortFunc(a.snapshot.ResultSets, resultSetCompare)
	metadata := a.snapshot
	metadata.ResultSets = make([]ResultSet, len(a.snapshot.ResultSets))
	for i, set := range a.snapshot.ResultSets {
		set.Diagnostics = []Diagnostic{}
		metadata.ResultSets[i] = set
	}
	// Include every document-set identity, and reserve space for limit notices
	// added below plus the next sequence digit. Findings never borrow the v1
	// 1 MiB metadata reservation, even with many documents/providers.
	budget := min(m.limits.FindingBytes, m.limits.SnapshotBytes-encodedSize(metadata)-32*1024)
	stopped := false
	for i := range a.snapshot.ResultSets {
		s := &a.snapshot.ResultSets[i]
		keep := 0
		for _, d := range s.Diagnostics {
			n := encodedSize(d) + 1
			// Also ensure a single finding fits an event carrying all metadata.
			if stopped || count >= m.limits.Findings || bytes+n > budget || n > m.limits.EventBytes/2 {
				stopped = true
				break
			}
			count++
			bytes += n
			keep++
		}
		if keep < len(s.Diagnostics) {
			// Copy the retained prefix: discarded strings/ranges must not remain in a
			// backing array after a large/truncated run.
			kept := make([]Diagnostic, keep)
			copy(kept, s.Diagnostics[:keep])
			s.Diagnostics = kept
			p := provider(a, s.ProviderID)
			if p == nil {
				continue
			}
			p.Truncated = true
			p.Coverage.Partial = true
			p.Notice = "Diagnostic retention limit reached; snapshot contains a bounded partial prefix"
		}
	}
}

// Counts projects exact display equivalence, independent of provider/source or
// freshness. Raw result sets remain separate so clearing one preserves others.
type Counts struct {
	Errors   int
	Warnings int
}

func CountFindings(s Snapshot) Counts {
	seen := map[string]bool{}
	out := Counts{}
	for _, set := range s.ResultSets {
		for _, d := range set.Diagnostics {
			key, _ := json.Marshal(struct {
				Path                    string
				Range                   *Range
				Severity, Message, Code string
			}{d.Path, d.Range, d.Severity, d.Message, d.Code})
			if seen[string(key)] {
				continue
			}
			seen[string(key)] = true
			if d.Severity == "error" {
				out.Errors++
			}
			if d.Severity == "warning" {
				out.Warnings++
			}
		}
	}
	return out
}
