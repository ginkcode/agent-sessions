package codediagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type fakeDocs struct {
	mu    sync.Mutex
	gen   string
	caps  ServerCapabilities
	err   error // Session error
	apply error
	steps []DocumentStep
}

func (f *fakeDocs) Session(AuthorizedRoot, string) (string, ServerCapabilities, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen, f.caps, f.err
}

func (f *fakeDocs) OwnerSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	gen, caps, err := f.Session(root, path)
	return DocumentOwnerIdentity{goplsProviderID, "fake-policy"}, gen, caps, err
}

func (f *fakeDocs) Apply(_ context.Context, s DocumentStep) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apply != nil {
		return f.apply
	}
	f.steps = append(f.steps, s)
	return nil
}

func (f *fakeDocs) kinds() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.steps {
		out = append(out, s.Kind)
	}
	return strings.Join(out, ",")
}

func (f *fakeDocs) last() DocumentStep {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.steps[len(f.steps)-1]
}

func docFixture(t *testing.T, caps ServerCapabilities) (*Manager, Snapshot, *fakeDocs) {
	t.Helper()
	m, snap, _ := managerFixture(t, ManagerLimits{})
	f := &fakeDocs{gen: "g1", caps: caps}
	m.SetDocumentProvider(f)
	return m, snap, f
}

var incrementalCaps = ServerCapabilities{OpenClose: true, Change: 2, Save: true, PositionEncoded: "utf-16"}

func errCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "other:" + err.Error()
}

func TestDocumentUnsupportedWithoutProviderStartsNothing(t *testing.T) {
	m, snap, _ := managerFixture(t, ManagerLimits{})
	_, err := m.OpenDocument(context.Background(), snap.Context, "source.go", "d1", 1, "x", "")
	if errCode(err) != "unsupported" {
		t.Fatal(err)
	}
	if got, _ := m.GetSnapshot(snap.Context); len(got.ResultSets) != 0 {
		t.Fatal("state created")
	}
}

func TestDocumentLifecycleVersionsAndIdempotence(t *testing.T) {
	m, snap, f := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "package a\r\n// 😀\r\n", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "x", ""); errCode(err) != "invalidArgument" {
		t.Fatal("duplicate open", err)
	}
	if _, err := m.OpenDocument(ctx, c, "source.go", "d2", 1, "x", ""); errCode(err) != "invalidArgument" {
		t.Fatal("second doc for path", err)
	}
	if _, err := m.OpenDocument(ctx, c, "source.go", "d3", 2, "x", ""); errCode(err) != "invalidArgument" {
		t.Fatal("version must be 1", err)
	}
	r, err := m.ChangeDocument(ctx, c, "d1", 3, "package a\r\n// 😀x\r\n")
	if err != nil || r.Version != 3 {
		t.Fatal(r, err)
	}
	last := f.last()
	if last.Kind != "change" || last.Version != 3 || last.Changes[0].Range == nil {
		t.Fatalf("%+v", last)
	}
	// Insert after the surrogate pair: line 1, UTF-16 column 5 ("// " + 2 units).
	if p := last.Changes[0].Range.Start; p.Line != 1 || p.Character != 5 {
		t.Fatalf("utf16 position %+v", p)
	}
	n := len(f.steps)
	if _, err = m.ChangeDocument(ctx, c, "d1", 3, "package a\r\n// 😀x\r\n"); err != nil || len(f.steps) != n {
		t.Fatal("repeat must be idempotent and silent", err)
	}
	if _, err = m.ChangeDocument(ctx, c, "d1", 3, "different"); errCode(err) != "invalidArgument" {
		t.Fatal("conflict", err)
	}
	if _, err = m.ChangeDocument(ctx, c, "d1", 2, "older"); errCode(err) != "invalidArgument" {
		t.Fatal("nonmonotonic", err)
	}
	if _, err = m.ChangeDocument(ctx, c, "d1", 3, "different"); err == nil || f.last().Version != 3 {
		t.Fatal("rejected change reached server")
	}
	// Not necessarily consecutive.
	if _, err = m.ChangeDocument(ctx, c, "d1", 9, "nine"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CloseDocument(ctx, c, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CloseDocument(ctx, c, "d1"); err != nil {
		t.Fatal("duplicate close must be idempotent", err)
	}
	if _, err = m.ChangeDocument(ctx, c, "d1", 10, "z"); errCode(err) != "staleContext" {
		t.Fatal("closed ID", err)
	}
	if _, err = m.OpenDocument(ctx, c, "source.go", "d1", 1, "z", ""); errCode(err) != "invalidArgument" {
		t.Fatal("closed ID reuse", err)
	}
	if _, err = m.OpenDocument(ctx, c, "source.go", "d4", 1, "fresh", ""); err != nil {
		t.Fatal("explicit reopen with fresh ID", err)
	}
	if got := f.kinds(); got != "open,change,change,close,open" {
		t.Fatal(got)
	}
	if _, err = m.CloseDocument(ctx, c, "never"); errCode(err) != "notFound" {
		t.Fatal(err)
	}
}

func TestDocumentFullSyncAndOldContexts(t *testing.T) {
	m, snap, f := docFixture(t, ServerCapabilities{OpenClose: true, Change: 1})
	ctx, c := context.Background(), snap.Context
	_, _ = m.OpenDocument(ctx, c, "source.go", "d", 1, "abc", "")
	if _, err := m.ChangeDocument(ctx, c, "d", 2, "abXc"); err != nil {
		t.Fatal(err)
	}
	if ch := f.last().Changes; len(ch) != 1 || ch[0].Range != nil || ch[0].Text != "abXc" {
		t.Fatalf("%+v", ch)
	}
	bad := c
	bad.Generation = "old"
	if _, err := m.ChangeDocument(ctx, bad, "d", 3, "q"); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
	other := c
	other.AttachmentID = "x"
	if _, err := m.CloseDocument(ctx, other, "d"); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
	// Revoke drops documents and rejects the identity under the new generation.
	rv, err := m.Revoke(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ChangeDocument(ctx, rv.Context, "d", 3, "q"); errCode(err) != "approvalRequired" {
		t.Fatal("revoked document accepted", err)
	}
	if _, err = m.CloseDocument(ctx, rv.Context, "d"); errCode(err) != "notFound" && errCode(err) != "" {
		t.Fatal("revoked documents must be dropped", err)
	}
	if s, _ := m.GetSnapshot(rv.Context); len(s.ResultSets) != 0 {
		t.Fatal("live sets survived revoke")
	}
	if _, err = m.OpenDocument(ctx, rv.Context, "source.go", "n", 1, "x", ""); errCode(err) != "approvalRequired" {
		t.Fatal("revoked approval must gate", err)
	}
}

func TestDocumentSaveHasNoBodyAndNoWrite(t *testing.T) {
	m, snap, f := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	root := filepath.Join(m.attachments[c.AttachmentID].root.Project().Root, "source.go")
	before, _ := os.ReadFile(root)
	info, _ := os.Stat(root)
	_, _ = m.OpenDocument(ctx, c, "source.go", "d", 1, "one", "")
	_, _ = m.ChangeDocument(ctx, c, "d", 2, "two")
	if _, err := m.SaveDocument(ctx, c, "d", 2, "rev2"); err != nil {
		t.Fatal(err)
	}
	if s := f.last(); s.Kind != "save" || s.Text != "" || s.Changes != nil || s.Revision != "rev2" {
		t.Fatalf("%+v", s)
	}
	n := len(f.steps)
	if _, err := m.SaveDocument(ctx, c, "d", 2, "rev2"); err != nil || len(f.steps) != n {
		t.Fatal("repeat save must be idempotent", err)
	}
	// Typing continued: an older saved version cannot be described without a body.
	_, _ = m.ChangeDocument(ctx, c, "d", 3, "three")
	n = len(f.steps)
	if _, err := m.SaveDocument(ctx, c, "d", 2, "rev3"); errCode(err) != "resyncRequired" || len(f.steps) != n {
		t.Fatal(err)
	}
	if _, err := m.SaveDocument(ctx, c, "d", 7, "r"); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
	if _, err := m.SaveDocument(ctx, c, "d", 3, ""); errCode(err) != "invalidArgument" {
		t.Fatal("revision required", err)
	}
	after, _ := os.ReadFile(root)
	info2, _ := os.Stat(root)
	if string(before) != string(after) || !info.ModTime().Equal(info2.ModTime()) {
		t.Fatal("file touched")
	}
	// Save not negotiated: honest local-only success, nothing sent.
	f.caps.Save = false
	n = len(f.steps)
	if _, err := m.SaveDocument(ctx, c, "d", 3, "rev4"); err != nil || len(f.steps) != n {
		t.Fatal(err)
	}
}

func TestDocumentFailureInvalidatesAndRequiresResync(t *testing.T) {
	m, snap, f := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	_, _ = m.OpenDocument(ctx, c, "source.go", "d", 1, "one", "")
	f.apply = &Error{"disconnected", "dropped"}
	if _, err := m.ChangeDocument(ctx, c, "d", 2, "two"); errCode(err) != "disconnected" {
		t.Fatal(err)
	}
	f.apply = nil
	if _, err := m.ChangeDocument(ctx, c, "d", 3, "three"); errCode(err) != "resyncRequired" {
		t.Fatal("dropped change must invalidate", err)
	}
	if _, err := m.SaveDocument(ctx, c, "d", 1, "r"); errCode(err) != "resyncRequired" {
		t.Fatal(err)
	}
	if _, err := m.CloseDocument(ctx, c, "d"); err != nil {
		t.Fatal(err)
	}
	// Open failure leaves no registry entry.
	f.apply = &Error{"limitExceeded", "big"}
	if _, err := m.OpenDocument(ctx, c, "source.go", "e", 1, "x", ""); errCode(err) != "limitExceeded" {
		t.Fatal(err)
	}
	f.apply = nil
	if _, err := m.OpenDocument(ctx, c, "source.go", "e2", 1, "x", ""); err != nil {
		t.Fatal(err)
	}
	// Session restart (new generation): change re-opens with current text; save needs resync.
	f.gen = "g2"
	if _, err := m.ChangeDocument(ctx, c, "e2", 2, "xy"); err != nil {
		t.Fatal(err)
	}
	if s := f.last(); s.Kind != "open" || s.Text != "xy" || s.Generation != "g2" {
		t.Fatalf("%+v", s)
	}
	f.gen = "g3"
	if _, err := m.SaveDocument(ctx, c, "e2", 2, "r"); errCode(err) != "resyncRequired" {
		t.Fatal(err)
	}
	f.err = &Error{"staleContext", "gone"}
	if _, err := m.ChangeDocument(ctx, c, "e2", 3, "z"); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
}

func TestDocumentValidationAndBounds(t *testing.T) {
	m, snap, _ := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	cases := []struct {
		path, text, want string
	}{
		{"../x", "a", "invalidArgument"},
		{"missing.go", "a", "notFound"},
		{"source.go", "bad\xff", "invalidArgument"},
		{"source.go", "nul\x00", "invalidArgument"},
		{"source.go", strings.Repeat("a", MaxDocumentBytes+1), "limitExceeded"},
	}
	for i, tc := range cases {
		if _, err := m.OpenDocument(ctx, c, tc.path, "id"+string(rune('a'+i)), 1, tc.text, ""); errCode(err) != tc.want {
			t.Fatalf("%q: %v", tc.path, err)
		}
	}
	if _, err := m.OpenDocument(ctx, c, "source.go", "", 1, "a", ""); errCode(err) != "invalidArgument" {
		t.Fatal(err)
	}
	if _, err := m.OpenDocument(ctx, c, "source.go", "ok", 1, strings.Repeat("a", MaxDocumentBytes), ""); err != nil {
		t.Fatal("max text", err)
	}
	if got, want := MaxDocumentBytes*6, 30<<20; got != want {
		t.Fatal("worst-case escaping stays under the 64 MiB frame")
	}
}

func TestPublicationsReplaceOnlyTheirDocument(t *testing.T) {
	m, snap, f := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	// A second project file is not needed: use two attachments' paths via two docs.
	dir := m.attachments[c.AttachmentID].root.Project().Root
	if err := os.WriteFile(filepath.Join(dir, "other.go"), []byte("o"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = m.OpenDocument(ctx, c, "source.go", "a", 1, "abc\n", "")
	_, _ = m.OpenDocument(ctx, c, "other.go", "b", 1, "xyz\n", "")
	one := int64(1)
	finding := func(path string, r *Range) Diagnostic {
		return Diagnostic{Path: path, Severity: "error", Message: "m", Source: "lsp", Range: r}
	}
	if err := m.PublishDocumentResults(c, "a", "g1", &one, []Diagnostic{finding("source.go", &Range{End: Position{0, 2}}), finding("source.go", &Range{End: Position{9, 0}}), finding("other.go", nil)}); err != nil {
		t.Fatal(err)
	}
	if err := m.PublishDocumentResults(c, "b", "g1", &one, []Diagnostic{finding("other.go", nil)}); err != nil {
		t.Fatal(err)
	}
	s, _ := m.GetSnapshot(c)
	if len(s.ResultSets) != 2 {
		t.Fatal(len(s.ResultSets))
	}
	slices.SortFunc(s.ResultSets, func(a, b ResultSet) int { return strings.Compare(a.Scope.DocumentID, b.Scope.DocumentID) })
	a := s.ResultSets[0]
	if a.Scope.DocumentID != "a" || len(a.Diagnostics) != 2 || a.Diagnostics[0].Freshness != "current" || a.Diagnostics[0].LocationIssue != "invalidRange" || a.Diagnostics[0].Range != nil {
		t.Fatalf("%+v", a)
	}
	// Empty publication clears only its own document.
	if err := m.PublishDocumentResults(c, "a", "g1", &one, nil); err != nil {
		t.Fatal(err)
	}
	s, _ = m.GetSnapshot(c)
	slices.SortFunc(s.ResultSets, func(a, b ResultSet) int { return strings.Compare(a.Scope.DocumentID, b.Scope.DocumentID) })
	if len(s.ResultSets[0].Diagnostics) != 0 || len(s.ResultSets[1].Diagnostics) != 1 {
		t.Fatalf("%+v", s.ResultSets)
	}
	// Late version, old generation, unversioned (uncertain, panel-only), closed doc.
	_, _ = m.ChangeDocument(ctx, c, "a", 2, "abcd\n")
	if err := m.PublishDocumentResults(c, "a", "g1", &one, nil); errCode(err) != "staleContext" {
		t.Fatal("late publication", err)
	}
	if err := m.PublishDocumentResults(c, "a", "old", nil, nil); errCode(err) != "staleContext" {
		t.Fatal(err)
	}
	if err := m.PublishDocumentResults(c, "a", "g1", nil, []Diagnostic{finding("source.go", &Range{End: Position{0, 1}})}); err != nil {
		t.Fatal(err)
	}
	s, _ = m.GetSnapshot(c)
	slices.SortFunc(s.ResultSets, func(a, b ResultSet) int { return strings.Compare(a.Scope.DocumentID, b.Scope.DocumentID) })
	if d := s.ResultSets[0].Diagnostics[0]; d.Freshness != "uncertain" || d.Range != nil {
		t.Fatalf("%+v", d)
	}
	_, _ = m.CloseDocument(ctx, c, "a")
	if err := m.PublishDocumentResults(c, "a", "g1", nil, nil); errCode(err) != "staleContext" {
		t.Fatal("closed publication", err)
	}
	s, _ = m.GetSnapshot(c)
	if len(s.ResultSets) != 1 || s.ResultSets[0].Scope.DocumentID != "b" {
		t.Fatalf("%+v", s.ResultSets)
	}
	_ = f
}

func TestDiffChangeCoordinates(t *testing.T) {
	cases := []struct{ old, new string }{
		{"a\r\nb", "a\r\nxb"},
		{"a\r\nb", "a\nb"},
		{"😀", "😁"},
		{"é😀x", "é😀yx"},
		{"", "q"},
		{"q", ""},
		{"same", "same"},
		{"a\r\n", "a\r\n\r\n"},
		{"x😀\r\n😀", "x😀😀"},
	}
	for _, tc := range cases {
		ch := diffChange(tc.old, tc.new)
		start, end, ok := offsets(tc.old, *ch.Range)
		if !ok || tc.old[:start]+ch.Text+tc.old[end:] != tc.new {
			t.Fatalf("%q -> %q: %+v", tc.old, tc.new, ch)
		}
	}
	if rangeInText("a😀b", Range{End: Position{0, 2}}) {
		t.Fatal("inside surrogate pair")
	}
	if !rangeInText("a😀b", Range{End: Position{0, 4}}) || rangeInText("a\r\nb", Range{End: Position{0, 2}}) || !rangeInText("a\r\nb", Range{Start: Position{1, 0}, End: Position{1, 1}}) {
		t.Fatal("coordinates")
	}
}

// offsets maps a valid range back to byte offsets independently of production code.
func offsets(text string, r Range) (int, int, bool) {
	find := func(p Position) (int, bool) {
		for i := 0; i <= len(text); i++ {
			if !validBoundary(text, i) {
				continue
			}
			if pos := positionOf(text, i); pos == p {
				return i, true
			}
		}
		return 0, false
	}
	s, ok1 := find(r.Start)
	e, ok2 := find(r.End)
	return s, e, ok1 && ok2
}

func validBoundary(text string, i int) bool {
	if i == 0 || i == len(text) {
		return true
	}
	return text[i]&0xC0 != 0x80 && (text[i-1] != '\r' || text[i] != '\n')
}

func TestDocumentsDroppedOnDetachAndClose(t *testing.T) {
	m, snap, f := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	_, _ = m.OpenDocument(ctx, c, "source.go", "d", 1, "x", "")
	if err := m.Detach(c); err != nil {
		t.Fatal(err)
	}
	m.Close() // joins the best-effort didClose worker
	if got := f.kinds(); got != "open,close" {
		t.Fatal(got)
	}
}

// managerHarness gives SessionDocuments tests an approved manager for a root
// that already has an approved session environment.
type managerHarness struct {
	*Manager
	snap Snapshot
}

func NewManagerForTest(t *testing.T, root AuthorizedRoot) *managerHarness {
	t.Helper()
	// Checker approval is separate from LSP session approval: reuse a checker tool.
	trust := NewTrust(nil)
	m, err := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	tools := []ToolInput{{ProviderID: "t", Executable: filepath.Join(root.Project().Root, "server"), ConfigPaths: []string{"cfg"}, Policy: "adapter-v1 fixed argv and env"}}
	snap, err := m.Attach(root, 1, []Registration{{ProviderID: "t", Availability: "available", Adapter: &fakeChecker{tools: tools, calls: make(chan adapterCall, 1)}}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = m.Approve(snap.Context, snap.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	return &managerHarness{m, snap}
}

func (h *managerHarness) snapshotFor(*testing.T) Snapshot { return h.snap }

func TestPublicationRejectsEvictedSessionEvenWithMatchingMirrorGeneration(t *testing.T) {
	m, snap, f := docFixture(t, incrementalCaps)
	ctx, c := context.Background(), snap.Context
	if _, err := m.OpenDocument(ctx, c, "source.go", "d1", 1, "draft", "r1"); err != nil {
		t.Fatal(err)
	}
	v := int64(1)
	f.mu.Lock()
	f.err = failure("staleContext", "Session expired")
	f.mu.Unlock()
	if err := m.PublishDocumentResults(c, "d1", "g1", &v, nil); errCode(err) != "staleContext" {
		t.Fatal("evicted session published", err)
	}
	f.mu.Lock()
	f.err, f.gen = nil, "g2"
	f.mu.Unlock()
	if err := m.PublishDocumentResults(c, "d1", "g1", &v, nil); errCode(err) != "staleContext" {
		t.Fatal("old session published after restart", err)
	}
	if _, err := m.ChangeDocument(ctx, c, "d1", 2, "actual displayed draft"); err != nil {
		t.Fatal(err)
	}
	if step := f.last(); step.Kind != "open" || step.Text != "actual displayed draft" || step.Generation != "g2" {
		t.Fatalf("restart must reopen displayed text: %+v", step)
	}
}

func TestDeletePathClosesLiveAndClearsEveryProvider(t *testing.T) {
	m, s, f := docFixture(t, ServerCapabilities{OpenClose: true, Change: 1})
	ctx := context.Background()
	if _, err := m.OpenDocument(ctx, s.Context, "source.go", "before", 1, "package main\n", "same"); err != nil {
		t.Fatal(err)
	}
	v := int64(1)
	if err := m.PublishDocumentResults(s.Context, "before", f.gen, &v, []Diagnostic{{Path: "source.go", Message: "live", Severity: "error"}}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	a := m.attachments[s.Context.AttachmentID]
	for _, id := range []string{"checker-a", "checker-b"} {
		a.snapshot.ResultSets = append(a.snapshot.ResultSets, ResultSet{ProviderID: id, Origin: "checker", Scope: Scope{Kind: "project"}, Diagnostics: []Diagnostic{{Path: "source.go", Message: "old"}, {Path: "other.go", Message: "keep"}}})
	}
	m.mu.Unlock()
	if err := m.DeletePath(s.Context, "source.go"); err != nil {
		t.Fatal(err)
	}
	if f.last().Kind != "close" {
		t.Fatal("didClose missing")
	}
	out, err := m.GetSnapshot(s.Context)
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range out.ResultSets {
		for _, d := range set.Diagnostics {
			if d.Path == "source.go" {
				t.Fatal("deleted finding retained")
			}
		}
	}
	code(t, m.PublishDocumentResults(s.Context, "before", f.gen, &v, nil), "staleContext")
	if _, err = m.OpenDocument(ctx, s.Context, "source.go", "after", 1, "package main\n", "same"); err != nil {
		t.Fatal(err)
	}
	code(t, m.PublishDocumentResults(s.Context, "before", f.gen, &v, nil), "staleContext")
	code(t, m.PublishDocumentResults(s.Context, "after", "old-generation", &v, nil), "staleContext")
}
