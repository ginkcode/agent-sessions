package codediagnostics

import (
	"context"
	"errors"
)

// DocumentServer is the fixed, adapter-owned choice of server and language for
// one project file. It is never decoded from a request.
type DocumentServer struct {
	Config     ServerConfig
	LanguageID string
}

// SessionDocuments adapts approved, already-running SessionManager sessions to
// DocumentProvider. It never launches a process: Session fails with
// unsupported when no adapter claims the path, and approvalRequired /
// staleContext when no current approved session exists.
type SessionDocuments struct {
	Sessions *SessionManager
	// Resolve is supplied by concrete adapters (CD-22…CD-24). Nil resolves nothing.
	Resolve func(root AuthorizedRoot, path string) (DocumentServer, bool)
}

func (d SessionDocuments) server(root AuthorizedRoot, path string) (DocumentServer, *lspSession, error) {
	if d.Sessions == nil || d.Resolve == nil {
		return DocumentServer{}, nil, failure("unsupported", "No language server adapter is available")
	}
	server, ok := d.Resolve(root, path)
	if !ok {
		return DocumentServer{}, nil, failure("unsupported", "No language server adapter is available for this file")
	}
	d.Sessions.mu.Lock()
	e := d.Sessions.entries[keyOf(root, server.Config)]
	var s *lspSession
	if e != nil {
		s = e.session
	}
	d.Sessions.mu.Unlock()
	if s == nil || !live(s.phaseNow()) || !s.current() {
		return DocumentServer{}, nil, failure("staleContext", "Language session is not running")
	}
	return server, s, nil
}

// OwnerSession preserves the resolver's concrete provider and exact policy.
func (d SessionDocuments) OwnerSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	server, s, err := d.server(root, path)
	if err != nil {
		return DocumentOwnerIdentity{}, "", ServerCapabilities{}, err
	}
	info := s.info()
	if info.Phase == phaseStarting {
		return DocumentOwnerIdentity{}, "", ServerCapabilities{}, failure("busy", "Language session is starting")
	}
	return DocumentOwnerIdentity{server.Config.ProviderID, server.Config.Policy()}, s.generation, info.Capabilities, nil
}

// Session reports the current generation and negotiated capabilities.
func (d SessionDocuments) Session(root AuthorizedRoot, path string) (string, ServerCapabilities, error) {
	_, s, err := d.server(root, path)
	if err != nil {
		return "", ServerCapabilities{}, err
	}
	info := s.info()
	if info.Phase == phaseStarting {
		return "", ServerCapabilities{}, failure("busy", "Language session is starting")
	}
	return s.generation, info.Capabilities, nil
}

// maxPublicationURIs bounds transport-lifetime URI history, independently of the
// app's closed-ID FIFO. Exhaustion disables all path publications until restart.
const maxPublicationURIs = 256

func (s *lspSession) recordPublicationURI(uri string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publicationFull {
		return
	}
	if reused, seen := s.publicationURIs[uri]; seen {
		if !reused {
			s.publicationURIs[uri] = true
		}
		return
	}
	if len(s.publicationURIs) >= maxPublicationURIs {
		s.publicationFull = true
		return
	}
	if s.publicationURIs == nil {
		s.publicationURIs = make(map[string]bool)
	}
	s.publicationURIs[uri] = false
}

// PublicationIsolated proves that exactly one open was attempted for this wire
// URI on the current transport. Matching versions cannot provide this proof.
func (d SessionDocuments) PublicationIsolated(root AuthorizedRoot, path, generation string) bool {
	_, s, err := d.server(root, path)
	if err != nil || s.generation != generation {
		return false
	}
	target, err := root.ResolvePath(root.Project().HostID, path)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reused, seen := s.publicationURIs[fileURI(target)]
	return seen && !reused && !s.publicationFull
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int64  `json:"version"`
	Text       string `json:"text"`
}

// Apply sends one ordered notification. Transport failures are reported as
// bounded errors, so a dropped change is never acknowledged.
func (d SessionDocuments) Apply(ctx context.Context, step DocumentStep) error {
	server, s, err := d.server(step.Root, step.Path)
	if err != nil {
		return err
	}
	if s.generation != step.Generation || !s.current() || step.Owner != (DocumentOwnerIdentity{server.Config.ProviderID, server.Config.Policy()}) {
		return failure("staleContext", "Language session generation changed")
	}
	target, err := step.Root.ResolvePath(step.Root.Project().HostID, step.Path)
	if err != nil {
		return err
	}
	uri := fileURI(target)
	tr := s.transport()
	if tr == nil {
		return failure("disconnected", "Language session is not connected")
	}
	var method string
	var params any
	switch step.Kind {
	case "open":
		// Record before attempting delivery: an interrupted write may have reached
		// the server. A failed open must never make the next open look isolated.
		s.recordPublicationURI(uri)
		method = "textDocument/didOpen"
		params = map[string]any{"textDocument": lspItem{uri, server.LanguageID, step.Version, step.Text}}
	case "change":
		method = "textDocument/didChange"
		changes := make([]map[string]any, 0, len(step.Changes))
		for _, c := range step.Changes {
			item := map[string]any{"text": c.Text}
			if c.Range != nil {
				item["range"] = map[string]any{
					"start": lspPosition{c.Range.Start.Line, c.Range.Start.Character},
					"end":   lspPosition{c.Range.End.Line, c.Range.End.Character},
				}
			}
			changes = append(changes, item)
		}
		params = map[string]any{"textDocument": map[string]any{"uri": uri, "version": step.Version}, "contentChanges": changes}
	case "save":
		method = "textDocument/didSave" // never includes text, by negotiation and by design
		params = map[string]any{"textDocument": map[string]any{"uri": uri}}
	case "close":
		method = "textDocument/didClose"
		params = map[string]any{"textDocument": map[string]any{"uri": uri}}
	default:
		return failure("invalidArgument", "Unknown document operation")
	}
	if err := tr.Notify(ctx, method, params); err != nil {
		switch {
		case errors.Is(err, ErrLSPFrameTooLarge):
			return failure("limitExceeded", "Document text exceeds the language session frame limit")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return failure("canceled", "Document synchronization was interrupted")
		}
		return failure("disconnected", "Language session dropped the document update")
	}
	return nil
}
