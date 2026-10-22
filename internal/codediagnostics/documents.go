package codediagnostics

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// Document limits (contract v1 §6). Text is measured before JSON escaping. The
// worst-case Go escaping of one byte is six bytes (for example U+2028 or a
// control character), so an escaped request stays below the 64 MiB app RPC
// frame: 5 MiB * 6 = 30 MiB plus a small envelope.
const (
	maxDocumentBytes   = 5 << 20
	maxDocuments       = 64
	maxClosedDocuments = 256
	maxPublished       = 10000
	documentIOTimeout  = 5 * time.Second
)

// MaxDocumentBytes is the decoded UTF-8 text limit shared by every route.
const MaxDocumentBytes = maxDocumentBytes

// DocumentReceipt is the only result of a document operation: identity, never text.
type DocumentReceipt struct {
	Context    Context `json:"context"`
	DocumentID string  `json:"documentId"`
	Version    int64   `json:"version,omitempty"`
}

// DocumentChange is one ordered edit of the session's copy of a document. Ranges
// are zero-based UTF-16 positions with exclusive end. A nil Range is the whole
// document.
type DocumentChange struct {
	Range *Range
	Text  string
}

// DocumentStep is one ordered synchronization notification. didSave never has a
// body; didClose has neither. Text is only set for open and full sync.
type DocumentStep struct {
	Root       AuthorizedRoot
	Generation string
	Kind       string // open, change, save, close
	Path       string
	Version    int64
	Text       string
	Changes    []DocumentChange
	Revision   string
}

// DocumentProvider is the host-local seam to approved language sessions. It
// never starts a process: a missing/stale session is reported as an error.
// Implementations must not log or retain text.
type DocumentProvider interface {
	// Session reports the current generation and negotiated synchronization.
	Session(root AuthorizedRoot, path string) (generation string, caps ServerCapabilities, err error)
	Apply(ctx context.Context, step DocumentStep) error
}

type documentState struct {
	path       string
	version    int64
	text       string // bounded mirror of the displayed text; never persisted or logged
	generation string // session generation that last received this document
	broken     bool   // a send failed or was ambiguous: only a fresh open recovers
	saved      int64  // last version reported through didSave
	revision   string
}

// SetDocumentProvider installs the session seam. Without one every document
// operation fails with unsupported before any process or state exists.
func (m *Manager) SetDocumentProvider(p DocumentProvider) {
	m.mu.Lock()
	m.provider = p
	m.mu.Unlock()
}

func (m *Manager) docStep(ctx context.Context, a *attachment, d *documentState, step DocumentStep) error {
	step.Root, step.Path = a.root, d.path
	cctx, cancel := context.WithTimeout(ctx, documentIOTimeout)
	defer cancel()
	return m.provider.Apply(cctx, step)
}

func (m *Manager) liveGate(a *attachment) error {
	if m.provider == nil {
		return failure("unsupported", "Live code diagnostics are unavailable")
	}
	if a.snapshot.Approval.State != "approved" {
		return failure("approvalRequired", "Explicit execution approval is required")
	}
	return nil
}

func checkText(text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return failure("invalidArgument", "Document text must be valid UTF-8 without NUL")
	}
	if len(text) > maxDocumentBytes {
		return failure("limitExceeded", "Document text exceeds the live diagnostics limit")
	}
	return nil
}

// OpenDocument registers actual displayed text under a fresh documentId and
// sends didOpen to the approved session. The mirror exists only to compute the
// negotiated incremental edit and to detect an idempotent repeat.
func (m *Manager) OpenDocument(ctx context.Context, c Context, path, documentID string, version int64, text, revision string) (DocumentReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return DocumentReceipt{}, err
	}
	if err = m.liveGate(a); err != nil {
		return DocumentReceipt{}, err
	}
	if !bounded(documentID, 256) || version != 1 || (revision != "" && !bounded(revision, 256)) {
		return DocumentReceipt{}, failure("invalidArgument", "Invalid document identity, version or revision")
	}
	if err = checkText(text); err != nil {
		return DocumentReceipt{}, err
	}
	if a.docs[documentID] != nil || a.closedDocs[documentID] {
		return DocumentReceipt{}, failure("invalidArgument", "Document identity was already used")
	}
	for _, d := range a.docs {
		if d.path == path {
			return DocumentReceipt{}, failure("invalidArgument", "Path already has an open document")
		}
	}
	if len(a.docs) >= maxDocuments {
		return DocumentReceipt{}, failure("limitExceeded", "Open document budget is full")
	}
	// Host-side containment (symlinks included) before any session sees the path.
	if _, err = a.root.ResolvePath(a.root.Project().HostID, path); err != nil {
		return DocumentReceipt{}, err
	}
	generation, _, err := m.provider.Session(a.root, path)
	if err != nil {
		return DocumentReceipt{}, err
	}
	d := &documentState{path: path, version: 1, text: text, generation: generation, revision: revision}
	if err = m.docStep(ctx, a, d, DocumentStep{Generation: generation, Kind: "open", Version: 1, Text: text}); err != nil {
		return DocumentReceipt{}, err
	}
	if a.docs == nil {
		a.docs = map[string]*documentState{}
	}
	a.docs[documentID] = d
	return DocumentReceipt{Context: c, DocumentID: documentID, Version: 1}, nil
}

func (m *Manager) ownedDocument(c Context, documentID string) (*attachment, *documentState, error) {
	a, err := m.lookup(c)
	if err != nil {
		return nil, nil, err
	}
	if err = m.liveGate(a); err != nil { // unsupported first: no state is consulted
		return nil, nil, err
	}
	if !bounded(documentID, 256) {
		return nil, nil, failure("invalidArgument", "Invalid document identity")
	}
	if a.closedDocs[documentID] {
		return nil, nil, failure("staleContext", "Document lifecycle is closed")
	}
	d := a.docs[documentID]
	if d == nil {
		return nil, nil, failure("notFound", "Document is not open")
	}
	return a, d, nil
}

// ChangeDocument takes full displayed text. Versions strictly increase. The
// same version with identical text is idempotent; different text conflicts. A
// failed or ambiguous send marks the document broken (resyncRequired) and the
// version is not accepted: a dropped change is never reported as delivered.
func (m *Manager) ChangeDocument(ctx context.Context, c Context, documentID string, version int64, text string) (DocumentReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, d, err := m.ownedDocument(c, documentID)
	if err != nil {
		return DocumentReceipt{}, err
	}
	if version < 1 || version > maxSequence {
		return DocumentReceipt{}, failure("invalidArgument", "Invalid document version")
	}
	if err = checkText(text); err != nil {
		return DocumentReceipt{}, err
	}
	if d.broken {
		return DocumentReceipt{}, failure("resyncRequired", "Document must be reopened")
	}
	if version < d.version {
		return DocumentReceipt{}, failure("invalidArgument", "Document version is not monotonic")
	}
	if version == d.version {
		if text != d.text {
			return DocumentReceipt{}, failure("invalidArgument", "Document version conflicts with different text")
		}
		return DocumentReceipt{Context: c, DocumentID: documentID, Version: version}, nil
	}
	generation, caps, err := m.provider.Session(a.root, d.path)
	if err != nil {
		d.broken = true
		return DocumentReceipt{}, err
	}
	step := DocumentStep{Generation: generation, Kind: "change", Version: version}
	switch {
	case generation != d.generation:
		// A new session never saw this document: reopen it with the current text.
		step.Kind, step.Text = "open", text
	case caps.FullSync():
		step.Changes = []DocumentChange{{Text: text}}
	case caps.IncrementalSync():
		step.Changes = []DocumentChange{diffChange(d.text, text)}
	default:
		d.broken = true
		return DocumentReceipt{}, failure("unsupported", "Language session synchronization is not negotiated")
	}
	if err = m.docStep(ctx, a, d, step); err != nil {
		d.broken = true
		return DocumentReceipt{}, err
	}
	d.version, d.text, d.generation = version, text, generation
	return DocumentReceipt{Context: c, DocumentID: documentID, Version: version}, nil
}

// SaveDocument reports that an editor save of savedVersion completed. It has no
// body and performs no file write. It only describes the live version: an older
// savedVersion cannot be expressed without a body, so it is resyncRequired and
// no didSave is sent (the newer draft is never claimed as saved).
func (m *Manager) SaveDocument(ctx context.Context, c Context, documentID string, savedVersion int64, revision string) (DocumentReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, d, err := m.ownedDocument(c, documentID)
	if err != nil {
		return DocumentReceipt{}, err
	}
	if !bounded(revision, 256) || savedVersion < 1 || savedVersion > d.version {
		return DocumentReceipt{}, failure("invalidArgument", "Invalid saved version or revision")
	}
	if d.broken {
		return DocumentReceipt{}, failure("resyncRequired", "Document must be reopened")
	}
	receipt := DocumentReceipt{Context: c, DocumentID: documentID}
	if d.saved == savedVersion && d.revision == revision {
		return receipt, nil
	}
	if savedVersion != d.version {
		return DocumentReceipt{}, failure("resyncRequired", "Saved snapshot is older than the live document")
	}
	generation, caps, err := m.provider.Session(a.root, d.path)
	if err != nil {
		return DocumentReceipt{}, err
	}
	if generation != d.generation {
		return DocumentReceipt{}, failure("resyncRequired", "Language session restarted; document must be synchronized again")
	}
	if caps.Save { // negotiated off: editor save is local only, nothing to send
		if err = m.docStep(ctx, a, d, DocumentStep{Generation: generation, Kind: "save", Version: savedVersion, Revision: revision}); err != nil {
			d.broken = true
			return DocumentReceipt{}, err
		}
	}
	d.saved, d.revision = savedVersion, revision
	return receipt, nil
}

// CloseDocument ends one lifecycle deterministically: owned live results are
// removed first, the identity is rejected afterwards, then didClose is sent
// best-effort. A repeated close of a closed identity is idempotent.
func (m *Manager) CloseDocument(ctx context.Context, c Context, documentID string) (DocumentReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return DocumentReceipt{}, err
	}
	if !bounded(documentID, 256) {
		return DocumentReceipt{}, failure("invalidArgument", "Invalid document identity")
	}
	receipt := DocumentReceipt{Context: c, DocumentID: documentID}
	if a.closedDocs[documentID] {
		return receipt, nil
	}
	d := a.docs[documentID]
	if d == nil {
		return DocumentReceipt{}, failure("notFound", "Document is not open")
	}
	if m.dropDocument(a, documentID) && m.provider != nil && !d.broken {
		_ = m.docStep(ctx, a, d, DocumentStep{Generation: d.generation, Kind: "close", Version: d.version})
	}
	m.commit(a)
	return receipt, nil
}

// dropDocument removes the registry entry and its LSP result set and remembers
// the identity (bounded FIFO) so it can never be reused or published to.
func (m *Manager) dropDocument(a *attachment, documentID string) bool {
	d := a.docs[documentID]
	if d == nil {
		return false
	}
	d.text = ""
	delete(a.docs, documentID)
	if a.closedDocs == nil {
		a.closedDocs = map[string]bool{}
	}
	a.closedDocs[documentID] = true
	a.closedOrder = append(a.closedOrder, documentID)
	if len(a.closedOrder) > maxClosedDocuments {
		delete(a.closedDocs, a.closedOrder[0])
		a.closedOrder = a.closedOrder[1:]
	}
	kept := make([]ResultSet, 0, len(a.snapshot.ResultSets))
	for _, s := range a.snapshot.ResultSets {
		if s.Origin == "lsp" && s.Scope.DocumentID == documentID {
			continue
		}
		kept = append(kept, s)
	}
	a.snapshot.ResultSets = kept
	return true
}

// releaseDocuments drops every document of an attachment (detach, revoke,
// invalidation). didClose is best-effort and runs outside the owner lock.
func (m *Manager) releaseDocuments(a *attachment) {
	if len(a.docs) == 0 {
		return
	}
	steps := []DocumentStep{}
	for id, d := range a.docs {
		if m.provider != nil && !d.broken {
			steps = append(steps, DocumentStep{Root: a.root, Path: d.path, Generation: d.generation, Kind: "close", Version: d.version})
		}
		m.dropDocument(a, id)
	}
	if p := m.provider; p != nil && len(steps) > 0 {
		m.workers.Add(1)
		go func() {
			defer m.workers.Done()
			for _, step := range steps {
				ctx, cancel := context.WithTimeout(context.Background(), documentIOTimeout)
				_ = p.Apply(ctx, step)
				cancel()
			}
		}()
	}
}

// PublishDocumentResults replaces exactly one open document's LSP set (empty
// included). The identity, path, session generation and version must match the
// current lifecycle, so a closed/reopened ID or late publication is rejected.
// Unversioned publications stay uncertain and panel-only; ranges are validated
// against the mirrored text and invalid ones become file-level findings.
func (m *Manager) PublishDocumentResults(c Context, documentID, generation string, version *int64, diagnostics []Diagnostic) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, d, err := m.ownedDocument(c, documentID)
	if err != nil {
		return err
	}
	return m.publishLocked(a, d, documentID, generation, version, diagnostics)
}

// PublishPathDiagnostics is the adapter seam: it finds the open document of the
// attachment owning project by path, then applies the same guards as
// PublishDocumentResults. A path with no open document (closed, never opened) is
// notFound, so a late server publication can never create state.
func (m *Manager) PublishPathDiagnostics(project Project, generation, path string, version *int64, diagnostics []Diagnostic) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return failure("disconnected", "Diagnostics owner is closed")
	}
	for _, a := range m.attachments {
		if a.root.Project() != project {
			continue
		}
		if m.liveGate(a) != nil {
			return failure("unsupported", "Live code diagnostics are unavailable")
		}
		for id, d := range a.docs {
			if d.path == path {
				return m.publishLocked(a, d, id, generation, version, diagnostics)
			}
		}
		return failure("notFound", "Document is not open")
	}
	return failure("staleContext", "Diagnostics attachment is no longer current")
}

func (m *Manager) publishLocked(a *attachment, d *documentState, documentID, generation string, version *int64, diagnostics []Diagnostic) error {
	// The document mirror can outlive an idle-evicted/disconnected session.
	// A matching historical generation alone is not proof that it is still live.
	current, _, err := m.provider.Session(a.root, d.path)
	if err != nil || current != generation {
		return failure("staleContext", "Language session is no longer current")
	}
	if generation != d.generation || d.broken || (version != nil && *version != d.version) {
		return failure("staleContext", "Document publication is not current")
	}
	out := []Diagnostic{}
	for _, in := range diagnostics {
		if len(out) >= maxPublished {
			break
		}
		if in.Path != d.path || in.Message == "" || !utf8.ValidString(in.Message) || !bounded(in.Source, 256) || (in.Code != "" && !bounded(in.Code, 256)) {
			continue
		}
		switch in.Severity {
		case "error", "warning", "information", "hint":
		default:
			continue
		}
		in.Message = strings.Clone(shorten(in.Message, 8192))
		in.Basis = SavedBasis{Kind: "document", DocumentID: documentID, Version: version}
		in.Freshness, in.LocationIssue = "uncertain", ""
		if version != nil {
			in.Freshness = "current"
		}
		if in.Range != nil {
			switch {
			case version == nil:
				in.Range = nil // cannot be proved against the displayed text
			case !rangeInText(d.text, *in.Range):
				in.Range, in.LocationIssue = nil, "invalidRange"
			default:
				r := *in.Range
				in.Range = &r
			}
		}
		out = append(out, in)
	}
	set := ResultSet{ProviderID: "lsp", Origin: "lsp", Scope: Scope{Kind: "document", Path: d.path, DocumentID: documentID}, Diagnostics: out}
	replaced := false
	for i := range a.snapshot.ResultSets {
		if s := a.snapshot.ResultSets[i]; s.Origin == "lsp" && s.Scope.DocumentID == documentID {
			a.snapshot.ResultSets[i], replaced = set, true
		}
	}
	if !replaced {
		a.snapshot.ResultSets = append(a.snapshot.ResultSets, set)
	}
	m.commit(a)
	return nil
}

// ---- UTF-16 coordinates over UTF-8 text -------------------------------------

// lineAt reports a line separator width at byte offset i (CRLF, CR or LF).
func separator(text string, i int) int {
	switch {
	case text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n':
		return 2
	case text[i] == '\r' || text[i] == '\n':
		return 1
	}
	return 0
}

// positionOf converts a byte offset (not inside CRLF or a rune) to line and
// UTF-16 column. CR/LF contribute no column; non-BMP characters count as two.
func positionOf(text string, offset int) Position {
	line, col := 0, 0
	for i := 0; i < offset; {
		if w := separator(text, i); w > 0 {
			line, col, i = line+1, 0, i+w
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		col++
		if r >= 0x10000 {
			col++
		}
		i += size
	}
	return Position{Line: line, Character: col}
}

// rangeInText validates ordered coordinates against actual text: the line must
// exist, the column must land on a rune boundary no later than the line end.
func rangeInText(text string, r Range) bool {
	if !validRange(&r) {
		return false
	}
	return coordinateInText(text, r.Start) && coordinateInText(text, r.End)
}

func coordinateInText(text string, p Position) bool {
	line, col := 0, 0
	for i := 0; ; {
		if line == p.Line && col == p.Character {
			return true
		}
		if i >= len(text) {
			return false
		}
		if w := separator(text, i); w > 0 {
			if line == p.Line {
				return false // column beyond the line end
			}
			line, col, i = line+1, 0, i+w
			continue
		}
		if line == p.Line && col > p.Character {
			return false // inside a surrogate pair
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		col++
		if r >= 0x10000 {
			col++
		}
		i += size
	}
}

// diffChange returns the single ordered edit turning old into new. Boundaries
// are moved off rune and CRLF interiors so positions are always valid.
func diffChange(old, new string) DocumentChange {
	runeStart := func(t string, i int) bool { return i >= len(t) || utf8.RuneStart(t[i]) }
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	for p > 0 && (!runeStart(old, p) || !runeStart(new, p)) {
		p--
	}
	if p > 0 && old[p-1] == '\r' && ((p < len(old) && old[p] == '\n') || (p < len(new) && new[p] == '\n')) {
		p--
	}
	s := 0
	for s < len(old)-p && s < len(new)-p && old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	for s > 0 && (!runeStart(old, len(old)-s) || !runeStart(new, len(new)-s)) {
		s--
	}
	if s > 0 {
		for _, t := range []string{old, new} {
			if i := len(t) - s; t[i] == '\n' && i > 0 && t[i-1] == '\r' {
				s--
				break
			}
		}
	}
	return DocumentChange{
		Range: &Range{Start: positionOf(old, p), End: positionOf(old, len(old)-s)},
		Text:  new[p : len(new)-s],
	}
}
