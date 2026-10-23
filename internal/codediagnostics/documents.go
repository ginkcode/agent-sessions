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
	Owner      DocumentOwnerIdentity
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

// DocumentOwnerIdentity is backend-resolved, never client or diagnostic input.
// Policy binds the exact fixed server configuration, independently of generation.
type DocumentOwnerIdentity struct {
	ProviderID string
	Policy     string
}

// OwnedDocumentProvider augments the legacy synchronization seam. Live operations
// fail closed without it: a generation alone cannot establish provider ownership.
type OwnedDocumentProvider interface {
	DocumentProvider
	OwnerSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error)
}

// DocumentSession is one independently owned synchronization route. Err is per
// provider so an unavailable peer cannot suppress a healthy session.
type DocumentSession struct {
	Owner        DocumentOwnerIdentity
	Generation   string
	Capabilities ServerCapabilities
	Err          error
}

type MultiOwnedDocumentProvider interface {
	DocumentProvider
	OwnerSessions(AuthorizedRoot, string) []DocumentSession
}

func (m *Manager) documentSessions(root AuthorizedRoot, path string) []DocumentSession {
	var entries []DocumentSession
	if p, ok := m.provider.(MultiOwnedDocumentProvider); ok {
		entries = p.OwnerSessions(root, path)
	} else {
		owner, generation, caps, err := m.documentSession(root, path)
		entries = []DocumentSession{{owner, generation, caps, err}}
	}
	seen := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		if !bounded(e.Owner.ProviderID, 256) || e.Owner.ProviderID == "lsp" || !bounded(e.Owner.Policy, 4096) || seen[e.Owner.ProviderID] || (e.Err == nil && !bounded(e.Generation, 256)) {
			return []DocumentSession{{Err: failure("unsupported", "Concrete document ownership is unavailable")}}
		}
		seen[e.Owner.ProviderID] = true
	}
	return entries
}

func (m *Manager) routeSession(root AuthorizedRoot, path string, owner DocumentOwnerIdentity) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	for _, e := range m.documentSessions(root, path) {
		if e.Owner == owner {
			return e.Owner, e.Generation, e.Capabilities, e.Err
		}
	}
	return DocumentOwnerIdentity{}, "", ServerCapabilities{}, failure("resyncRequired", "Document owner changed; document must be reopened")
}

func (m *Manager) documentSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	p, ok := m.provider.(OwnedDocumentProvider)
	if !ok {
		return DocumentOwnerIdentity{}, "", ServerCapabilities{}, failure("unsupported", "Concrete document ownership is unavailable")
	}
	owner, generation, caps, err := p.OwnerSession(root, path)
	if err == nil && (!bounded(owner.ProviderID, 256) || owner.ProviderID == "lsp" || !bounded(owner.Policy, 4096) || !bounded(generation, 256)) {
		err = failure("unsupported", "Concrete document ownership is unavailable")
	}
	return owner, generation, caps, err
}

type documentState struct {
	routes     []*documentState // non-nil only for a multi-provider frontend lifecycle
	owner      DocumentOwnerIdentity
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
	if len(d.routes) > 0 {
		var first error
		for _, route := range d.routes {
			next := step
			next.Generation = route.generation
			if err := m.docStep(ctx, a, route, next); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	step.Root, step.Path, step.Owner = a.root, d.path, d.owner
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
	if p, ok := m.provider.(interface {
		EnsureSession(context.Context, AuthorizedRoot, string) error
	}); ok {
		if err = p.EnsureSession(ctx, a.root, path); err != nil {
			return DocumentReceipt{}, err
		}
	}
	entries := m.documentSessions(a.root, path)
	var routes []*documentState
	var first error
	for _, entry := range entries {
		route := &documentState{owner: entry.Owner, path: path, version: 1, text: text, generation: entry.Generation, revision: revision}
		sendErr := entry.Err
		if sendErr == nil {
			sendErr = m.docStep(ctx, a, route, DocumentStep{Generation: entry.Generation, Kind: "open", Version: 1, Text: text})
		}
		if sendErr != nil {
			route.broken = true
			if first == nil {
				first = sendErr
			}
		}
		routes = append(routes, route)
	}
	healthy := false
	for _, route := range routes {
		healthy = healthy || !route.broken
	}
	if !healthy {
		// Opens may have reached a server even when delivery reported an error.
		// Close every attempted generation; transport URI history remains tainted.
		for _, route := range routes {
			if route.generation != "" {
				_ = m.docStep(ctx, a, route, DocumentStep{Generation: route.generation, Kind: "close", Version: 1})
			}
		}
		for _, route := range routes {
			m.documentRouteFailed(a, route.owner.ProviderID)
		}
		m.commit(a)
		if first == nil {
			first = failure("unsupported", "No language server owns this file")
		}
		return DocumentReceipt{}, first
	}
	d := routes[0]
	if len(routes) > 1 {
		d = &documentState{path: path, version: 1, text: text, revision: revision, routes: routes}
	}
	if a.docs == nil {
		a.docs = map[string]*documentState{}
	}
	a.docs[documentID] = d
	for i := range a.snapshot.Providers {
		p := &a.snapshot.Providers[i]
		if documentHasProvider(d, p.ProviderID) && p.Coverage.Kind == "openedDocuments" {
			failed := false
			for _, route := range d.routes {
				if route.owner.ProviderID == p.ProviderID && route.broken {
					failed = true
				}
			}
			if failed {
				m.documentRouteFailed(a, p.ProviderID)
				continue
			}
			p.Phase, p.Freshness, p.Failure = "idle", "uncertain", nil
			p.Notice = "Diagnostics cover opened documents only; external changes are unwatched."
		}
	}
	m.commit(a)
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
	if len(d.routes) > 0 {
		if version < d.version || (version == d.version && text != d.text) {
			return DocumentReceipt{}, failure("invalidArgument", "Document version conflicts")
		}
		if version == d.version {
			return DocumentReceipt{Context: c, DocumentID: documentID, Version: version}, nil
		}
		var first error
		healthy := false
		for _, route := range d.routes {
			_, err := m.changeDocumentRoute(ctx, c, a, route, documentID, version, text)
			if err == nil {
				healthy = true
			} else {
				m.dropProviderDocumentResults(a, documentID, route.owner.ProviderID)
				if first == nil {
					first = err
				}
			}
		}
		if !healthy {
			return DocumentReceipt{}, first
		}
		d.version, d.text = version, text
		return DocumentReceipt{Context: c, DocumentID: documentID, Version: version}, nil
	}
	return m.changeDocumentRoute(ctx, c, a, d, documentID, version, text)
}

func (m *Manager) changeDocumentRoute(ctx context.Context, c Context, a *attachment, d *documentState, documentID string, version int64, text string) (DocumentReceipt, error) {
	var err error
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
	owner, generation, caps, err := m.routeSession(a.root, d.path, d.owner)
	if err == nil && owner != d.owner {
		d.broken = true
		err = failure("resyncRequired", "Document owner changed; document must be reopened")
	}
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
	if len(d.routes) > 0 {
		if savedVersion != d.version {
			return DocumentReceipt{}, failure("resyncRequired", "Saved snapshot is older than the live document")
		}
		var first error
		healthy := false
		for _, route := range d.routes {
			_, err := m.saveDocumentRoute(ctx, c, a, route, documentID, savedVersion, revision)
			if err == nil {
				healthy = true
			} else {
				m.dropProviderDocumentResults(a, documentID, route.owner.ProviderID)
				if first == nil {
					first = err
				}
			}
		}
		if !healthy {
			return DocumentReceipt{}, first
		}
		d.saved, d.revision = savedVersion, revision
		return DocumentReceipt{Context: c, DocumentID: documentID}, nil
	}
	return m.saveDocumentRoute(ctx, c, a, d, documentID, savedVersion, revision)
}

func (m *Manager) saveDocumentRoute(ctx context.Context, c Context, a *attachment, d *documentState, documentID string, savedVersion int64, revision string) (DocumentReceipt, error) {
	var err error
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
	owner, generation, caps, err := m.routeSession(a.root, d.path, d.owner)
	if err == nil && owner != d.owner {
		d.broken = true
		err = failure("resyncRequired", "Document owner changed; document must be reopened")
	}
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
	m.idleDocuments(a)
	m.commit(a)
	return receipt, nil
}

// dropDocument removes the registry entry and its LSP result set and remembers
// recent identities in a bounded FIFO. Wire lifecycle isolation must not rely on
// this history: it is owned independently by the server transport.
func (m *Manager) dropDocument(a *attachment, documentID string) bool {
	d := a.docs[documentID]
	if d == nil {
		return false
	}
	d.text = ""
	for _, route := range d.routes {
		route.text = ""
	}
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
	a.dropGrants()
	if len(a.docs) == 0 {
		return
	}
	steps := []DocumentStep{}
	for id, d := range a.docs {
		if m.provider != nil && !d.broken {
			routes := d.routes
			if len(routes) == 0 {
				routes = []*documentState{d}
			}
			for _, route := range routes {
				steps = append(steps, DocumentStep{Root: a.root, Path: route.path, Owner: route.owner, Generation: route.generation, Kind: "close", Version: route.version})
			}
		}
		m.dropDocument(a, id)
	}
	if p := m.provider; p != nil && len(steps) > 0 {
		m.workers.Add(1)
		m.ownedWork.Add(1)
		go func() {
			defer m.workers.Done()
			defer m.ownedWork.Add(-1)
			for _, step := range steps {
				ctx, cancel := context.WithTimeout(context.Background(), documentIOTimeout)
				_ = p.Apply(ctx, step)
				cancel()
			}
		}()
	}
}

// PublishDocumentResults replaces exactly one open document's LSP set (empty
// included). The explicit identity, bound owner/policy, session generation and
// version must match. Only callers with actual lifecycle provenance may use this
// seam; wire adapters must use the isolation-guarded path route.
// Unversioned publications stay uncertain and panel-only; ranges are validated
// against the mirrored text and invalid ones become file-level findings.
func (m *Manager) PublishDocumentResults(c Context, documentID, generation string, version *int64, diagnostics []Diagnostic) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, d, err := m.ownedDocument(c, documentID)
	if err != nil {
		return err
	}
	// Compatibility for explicit lifecycle callers: derive the owner only from the
	// bound backend lifecycle, never from a finding's Source. Adapters must use
	// PublishPathDiagnostics with their concrete notification provenance.
	if len(d.routes) > 0 {
		return failure("unsupported", "Publication requires an explicit provider")
	}
	return m.publishLocked(a, d, documentID, d.owner.ProviderID, generation, version, diagnostics, false, false)
}

// PublishPathDiagnostics is the adapter seam: it finds the open document of the
// attachment owning project by path, then verifies the concrete session-envelope
// provider against the bound owner before any replacement. This is a backend-only
// seam; providerID must never come from a client or Diagnostic.Source. A path with
// no open document (closed, never opened) is notFound, so a late server publication
// can never create state.
func (m *Manager) PublishPathDiagnostics(project Project, providerID, generation, path string, version *int64, diagnostics []Diagnostic) error {
	return m.publishPathDiagnostics(project, providerID, generation, path, version, diagnostics, false, false)
}

func (m *Manager) publishPathDiagnostics(project Project, providerID, generation, path string, version *int64, diagnostics []Diagnostic, partial, truncated bool) error {
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
				// Wire pushes carry no documentId. Require transport-owned proof,
				// independent of attachment epochs, versions and closed-ID history.
				isolation, ok := m.provider.(interface {
					PublicationIsolated(AuthorizedRoot, string, string) bool
				})
				isolated := ok && isolation.PublicationIsolated(a.root, path, generation)
				if plural, ok := m.provider.(interface {
					PublicationIsolatedFor(AuthorizedRoot, string, string, string) bool
				}); ok {
					isolated = plural.PublicationIsolatedFor(a.root, path, providerID, generation)
				} else if len(d.routes) > 0 {
					isolated = false
				}
				if !isolated {
					return failure("resyncRequired", "Document publication lifecycle is ambiguous; results are unavailable until session restart")
				}
				return m.publishLocked(a, d, id, providerID, generation, version, diagnostics, partial, truncated)
			}
		}
		return failure("notFound", "Document is not open")
	}
	return failure("staleContext", "Diagnostics attachment is no longer current")
}

func (m *Manager) publishLocked(a *attachment, d *documentState, documentID, providerID, generation string, version *int64, diagnostics []Diagnostic, partial, truncated bool) error {
	if len(d.routes) > 0 {
		var route *documentState
		for _, candidate := range d.routes {
			if candidate.owner.ProviderID == providerID {
				route = candidate
				break
			}
		}
		if route == nil || route.version != d.version {
			return failure("staleContext", "Publication provider is not synchronized")
		}
		d = route
	}
	if providerID != d.owner.ProviderID {
		return failure("staleContext", "Publication provider does not own this document")
	}
	// The document mirror can outlive an idle-evicted/disconnected session.
	// A matching historical generation alone is not proof that it is still live.
	owner, current, _, err := m.routeSession(a.root, d.path, d.owner)
	if err != nil || owner != d.owner || current != generation {
		return failure("staleContext", "Language session is no longer current")
	}
	if generation != d.generation || d.broken || (version != nil && *version != d.version) {
		return failure("staleContext", "Document publication is not current")
	}
	bounded := boundedDiagnostics{}
	for _, candidate := range diagnostics {
		in, ok, limited := normalizeLiveDiagnostic(candidate, d, documentID, version)
		partial = partial || limited
		truncated = truncated || in.truncated
		if ok {
			bounded.add(in, m.limits)
		}
	}
	out := bounded.results()
	truncated = truncated || bounded.truncated
	if partial || truncated {
		p := provider(a, providerID)
		if p != nil {
			p.Truncated = p.Truncated || truncated
			p.Coverage.Partial = true
			p.Notice = "Some live findings were omitted, shortened or could not be verified; results are partial"
		}
	}
	set := ResultSet{ProviderID: providerID, Origin: "lsp", Scope: Scope{Kind: "document", Path: d.path, DocumentID: documentID}, Diagnostics: out}
	replaced := false
	for i := range a.snapshot.ResultSets {
		if s := a.snapshot.ResultSets[i]; s.ProviderID == set.ProviderID && s.Origin == set.Origin && s.Scope == set.Scope {
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

func documentHasProvider(d *documentState, id string) bool {
	if d.owner.ProviderID == id {
		return true
	}
	for _, route := range d.routes {
		if route.owner.ProviderID == id {
			return true
		}
	}
	return false
}

func (m *Manager) documentRouteFailed(a *attachment, providerID string) {
	if p := provider(a, providerID); p != nil {
		p.Phase, p.Freshness, p.Coverage.Partial = "failed", "stale", true
		p.Failure = &Error{Code: "resyncRequired", Message: "Document synchronization failed"}
		p.Notice = "Live document synchronization failed; this provider requires a fresh document lifecycle."
	}
}

func (m *Manager) dropProviderDocumentResults(a *attachment, documentID, providerID string) {
	m.documentRouteFailed(a, providerID)
	kept := a.snapshot.ResultSets[:0]
	for _, set := range a.snapshot.ResultSets {
		if set.Origin == "lsp" && set.ProviderID == providerID && set.Scope.DocumentID == documentID {
			continue
		}
		kept = append(kept, set)
	}
	a.snapshot.ResultSets = kept
	m.commit(a)
}
