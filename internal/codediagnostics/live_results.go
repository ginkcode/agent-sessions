package codediagnostics

import (
	"container/heap"
	"slices"
	"strings"
	"unicode/utf8"
)

// boundedDiagnostics selects a sorted prefix without retaining an unbounded
// candidate batch. Sizes use the actual Go JSON escaping, not UTF-8 lengths.
// The cutoff prevents a later small item from filling a hole after a larger,
// earlier item exhausted the byte budget (which would not be a prefix).
type boundedDiagnostics struct {
	h         diagnosticHeap
	bytes     int
	cutoff    *Diagnostic
	truncated bool
}

func (b *boundedDiagnostics) add(d Diagnostic, limits ManagerLimits) {
	if b.cutoff != nil && diagnosticCompare(d, *b.cutoff) >= 0 {
		b.truncated = true
		return
	}
	n := encodedSize(d) + 1
	heap.Push(&b.h, d)
	b.bytes += n
	for len(b.h) > limits.Findings || b.bytes > limits.FindingBytes {
		removed := heap.Pop(&b.h).(Diagnostic)
		b.bytes -= encodedSize(removed) + 1
		b.cutoff = &removed
		b.truncated = true
	}
}

func (b *boundedDiagnostics) results() []Diagnostic {
	out := []Diagnostic(b.h)
	slices.SortFunc(out, diagnosticCompare)
	if out == nil {
		return []Diagnostic{}
	}
	return out
}

func normalizeLiveDiagnostic(in Diagnostic, d *documentState, documentID string, version *int64) (Diagnostic, bool, bool) {
	if in.Path != d.path || in.Message == "" || !utf8.ValidString(in.Message) || !bounded(in.Source, 256) || (in.Code != "" && !bounded(in.Code, 256)) || !slices.Contains([]string{"error", "warning", "information", "hint"}, in.Severity) {
		return Diagnostic{}, false, true
	}
	partial := in.partial || len(in.Message) > 8192
	in.truncated = in.truncated || len(in.Message) > 8192
	in.Path, in.Source, in.Code = strings.Clone(d.path), strings.Clone(in.Source), strings.Clone(in.Code)
	in.Message = strings.Clone(shorten(in.Message, 8192))
	in.Basis = SavedBasis{Kind: "document", DocumentID: documentID}
	in.Freshness = "uncertain"
	if version != nil {
		v := *version // never retain the caller's mutable pointer
		in.Basis.Version = &v
		in.Freshness = "current"
	}
	if in.LocationIssue != "" && in.LocationIssue != "invalidRange" {
		in.LocationIssue, partial = "invalidRange", true
	}
	if in.LocationIssue == "invalidRange" {
		partial = true
	}
	// vtsls omits the optional publication version. Keep its ranges as uncertain
	// observations of this mirror, never as server-confirmed current results.
	observed := version == nil && d.owner.ProviderID == tsProviderID
	if observed {
		v := d.version
		in.Basis.ObservedVersion = &v
	}
	if in.Range != nil {
		switch {
		case version == nil && !observed:
			in.Range = nil
		case !rangeInText(d.text, *in.Range):
			in.Range, in.LocationIssue, partial = nil, "invalidRange", true
		default:
			r := *in.Range
			in.Range = &r
		}
	}
	return in, true, partial
}

// Carry adapter loss evidence atomically with the owned replacement, including
// batches whose every candidate was dropped. Legacy test sinks remain supported.
func publishLivePublication(sink goplsSink, n SessionNotification, pub goplsPublication) error {
	if m, ok := sink.(*Manager); ok {
		return m.publishPathDiagnostics(n.Project, n.ProviderID, n.Generation, pub.Path, pub.Version, pub.Diagnostics, pub.Dropped > 0 || pub.Partial, pub.Truncated)
	}
	return sink.PublishPathDiagnostics(n.Project, n.ProviderID, n.Generation, pub.Path, pub.Version, pub.Diagnostics)
}

func resultSetCompare(a, b ResultSet) int {
	for _, pair := range [][2]string{{a.ProviderID, b.ProviderID}, {a.Origin, b.Origin}, {a.Scope.Kind, b.Scope.Kind}, {a.Scope.Path, b.Scope.Path}, {a.Scope.DocumentID, b.Scope.DocumentID}} {
		if n := strings.Compare(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	return 0
}
