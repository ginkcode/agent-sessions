package codediagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Navigation operations: where a symbol is defined, where it is used, and
// what the server says about it.
const (
	NavigateDefinition = "definition"
	NavigateReferences = "references"
	NavigateHover      = "hover"
)

const (
	navigationTimeout      = 10 * time.Second
	maxNavigationLocations = 2000
	maxHoverBytes          = 64 << 10
)

// NavigationLocation is one place an answer points to. Path is project
// relative (slash separated) inside the root; outside it, Path is the
// absolute host path and External is set. Ranges are UTF-16, exclusive end.
type NavigationLocation struct {
	Path     string `json:"path"`
	Range    Range  `json:"range"`
	External bool   `json:"external,omitempty"`
}

// HoverText is the server's description of the symbol at a position.
type HoverText struct {
	Kind  string `json:"kind"` // markdown or plaintext
	Value string `json:"value"`
	Range *Range `json:"range,omitempty"`
}

// NavigationResult answers one request for the document at Version. Locations
// is empty, never nil, when the server knows no place; Hover is nil when it
// has nothing to say. Truncated reports dropped locations or hover text.
type NavigationResult struct {
	Op         string               `json:"op"`
	DocumentID string               `json:"documentId"`
	Version    int64                `json:"version"`
	Locations  []NavigationLocation `json:"locations"`
	Hover      *HoverText           `json:"hover,omitempty"`
	Truncated  bool                 `json:"truncated,omitempty"`
}

// NavigationRequest is one request to the session that holds the document at
// Generation, resolved by the Manager under its lock.
type NavigationRequest struct {
	Root       AuthorizedRoot
	Owner      DocumentOwnerIdentity
	Generation string
	Path       string
	Op         string
	Position   Position
}

// NavigationProvider sends navigation requests to running sessions. Like
// DocumentProvider it never starts a process.
type NavigationProvider interface {
	Navigate(ctx context.Context, req NavigationRequest) (NavigationResult, error)
}

func navigationOp(op string) bool {
	return op == NavigateDefinition || op == NavigateReferences || op == NavigateHover
}

// offers reports whether a session answers op.
func (c ServerCapabilities) offers(op string) bool {
	switch op {
	case NavigateDefinition:
		return c.Definition
	case NavigateReferences:
		return c.References
	case NavigateHover:
		return c.Hover
	}
	return false
}

func (c ServerCapabilities) navigation() int {
	n := 0
	for _, on := range []bool{c.Definition, c.References, c.Hover} {
		if on {
			n++
		}
	}
	return n
}

// Navigate asks the language server that holds an open document about the
// symbol at pos. The document must be open at exactly version, so the
// position means what the server has. A later edit can still reach the
// server before the request; the answer names the version it was asked for,
// and the caller drops it when the document moved on.
func (m *Manager) Navigate(ctx context.Context, c Context, documentID string, version int64, op string, pos Position) (NavigationResult, error) {
	if !navigationOp(op) {
		return NavigationResult{}, failure("invalidArgument", "Unknown navigation operation")
	}
	m.mu.Lock()
	req, nav, err := m.navigationRoute(c, documentID, version, op, pos)
	m.mu.Unlock()
	if err != nil {
		return NavigationResult{}, err
	}
	result, err := nav.Navigate(ctx, req)
	if err != nil {
		return NavigationResult{}, err
	}
	result.Op, result.DocumentID, result.Version = op, documentID, version
	if result.Locations == nil {
		result.Locations = []NavigationLocation{}
	}
	return result, nil
}

// navigationRoute picks the session that answers op for the document. Of
// several owners (Pyright and Ruff share Python files), the one offering the
// most navigation answers: a language server rather than a linter.
func (m *Manager) navigationRoute(c Context, documentID string, version int64, op string, pos Position) (NavigationRequest, NavigationProvider, error) {
	a, d, err := m.ownedDocument(c, documentID)
	if err != nil {
		return NavigationRequest{}, nil, err
	}
	nav, ok := m.provider.(NavigationProvider)
	if !ok {
		return NavigationRequest{}, nil, failure("unsupported", "Code navigation is unavailable")
	}
	if version != d.version {
		return NavigationRequest{}, nil, failure("staleVersion", "The document changed since this request")
	}
	if pos.Line < 0 || pos.Character < 0 || !coordinateInText(d.text, pos) {
		return NavigationRequest{}, nil, failure("invalidArgument", "Position is outside the document")
	}
	routes := d.routes
	if len(routes) == 0 {
		routes = []*documentState{d}
	}
	var best *documentState
	var bestGeneration string
	bestScore := 0
	var blocked error
	for _, route := range routes {
		if route.broken {
			blocked = failure("resyncRequired", "Document must be reopened")
			continue
		}
		owner, generation, caps, err := m.routeSession(a.root, route.path, route.owner)
		switch {
		case err != nil:
			blocked = navigationSessionError(err)
		case owner != route.owner || generation != route.generation || route.version != version:
			blocked = failure("resyncRequired", "The language server does not have this version; document must be reopened")
		case caps.offers(op) && caps.navigation() > bestScore:
			best, bestGeneration, bestScore = route, generation, caps.navigation()
		}
	}
	if best == nil {
		if blocked != nil {
			return NavigationRequest{}, nil, blocked
		}
		return NavigationRequest{}, nil, failure("unsupported", "The language server does not offer "+op)
	}
	return NavigationRequest{Root: a.root, Owner: best.owner, Generation: bestGeneration, Path: d.path, Op: op, Position: pos}, nav, nil
}

// navigationSessionError names a session that is not running as notRunning;
// busy (starting) and other codes pass through.
func navigationSessionError(err error) error {
	var e *Error
	if errors.As(err, &e) && e.Code == "staleContext" {
		return failure("notRunning", "The language server is not running")
	}
	return err
}

// Navigate sends one request to the running session that owns the document
// and normalizes its answer. Ten seconds without one is a timeout; the
// server is told to cancel.
func (d SessionDocuments) Navigate(ctx context.Context, req NavigationRequest) (NavigationResult, error) {
	server, s, err := d.serverFor(req.Root, req.Path, req.Owner)
	if err != nil {
		return NavigationResult{}, navigationSessionError(err)
	}
	if req.Owner != (DocumentOwnerIdentity{server.Config.ProviderID, server.Config.Policy()}) || s.generation != req.Generation || !s.current() {
		return NavigationResult{}, failure("resyncRequired", "Language session generation changed")
	}
	if !s.info().Capabilities.offers(req.Op) {
		return NavigationResult{}, failure("unsupported", "The language server does not offer "+req.Op)
	}
	target, err := req.Root.ResolvePath(req.Root.Project().HostID, req.Path)
	if err != nil {
		return NavigationResult{}, err
	}
	tr := s.transport()
	if tr == nil {
		return NavigationResult{}, failure("notRunning", "The language server is not running")
	}
	params := map[string]any{
		"textDocument": map[string]any{"uri": fileURI(target)},
		"position":     lspPosition{req.Position.Line, req.Position.Character},
	}
	method := "textDocument/" + req.Op
	if req.Op == NavigateReferences {
		params["context"] = map[string]any{"includeDeclaration": true}
	}
	cctx, cancel := s.deadline(ctx, navigationTimeout)
	defer cancel()
	raw, err := tr.Call(cctx, method, params)
	if err != nil {
		return NavigationResult{}, navigationCallError(cctx, err)
	}
	if req.Op == NavigateHover {
		return hoverResult(raw)
	}
	return locationResult(req.Root, raw)
}

const (
	lspRequestCancelled = -32800
	lspContentModified  = -32801
)

func navigationCallError(ctx context.Context, err error) error {
	var answer *LSPResponseError
	switch {
	case errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		return failure("timeout", "The language server did not answer in time")
	case ctx.Err() != nil:
		return failure("canceled", "Navigation was canceled")
	case errors.As(err, &answer) && answer.Code == lspContentModified:
		return failure("staleVersion", "The document changed while the language server answered")
	case errors.As(err, &answer) && answer.Code == lspRequestCancelled:
		return failure("canceled", "The language server canceled the request")
	case errors.As(err, &answer):
		return failure("toolFailed", "The language server could not answer")
	case errors.Is(err, ErrLSPTooManyPending):
		return failure("busy", "The language server has too many requests waiting")
	}
	return failure("disconnected", "Language session dropped the request")
}

// locationResult reads Location, Location[] or LocationLink[]. A link's
// target selection range (the name) is used, else its target range. Places
// that are not files, have bad ranges or cannot be resolved inside the root
// are left out.
func locationResult(root AuthorizedRoot, raw json.RawMessage) (NavigationResult, error) {
	var out NavigationResult
	var items []json.RawMessage
	switch t := strings.TrimSpace(string(raw)); {
	case t == "" || t == "null":
		return out, nil
	case t[0] == '[':
		if json.Unmarshal(raw, &items) != nil {
			return out, failure("toolFailed", "The language server answer is invalid")
		}
	default:
		items = []json.RawMessage{raw}
	}
	seen := map[NavigationLocation]bool{}
	for _, item := range items {
		var loc struct {
			URI                  string `json:"uri"`
			Range                *Range `json:"range"`
			TargetURI            string `json:"targetUri"`
			TargetRange          *Range `json:"targetRange"`
			TargetSelectionRange *Range `json:"targetSelectionRange"`
		}
		if json.Unmarshal(item, &loc) != nil {
			continue
		}
		uri, rng := loc.URI, loc.Range
		if loc.TargetURI != "" {
			uri, rng = loc.TargetURI, loc.TargetSelectionRange
			if rng == nil {
				rng = loc.TargetRange
			}
		}
		if rng == nil || !validRange(rng) {
			continue
		}
		path, external, ok := navigationPath(root, uri)
		if !ok {
			continue
		}
		place := NavigationLocation{Path: path, Range: *rng, External: external}
		if seen[place] {
			continue
		}
		if len(out.Locations) == maxNavigationLocations {
			out.Truncated = true
			break
		}
		seen[place] = true
		out.Locations = append(out.Locations, place)
	}
	return out, nil
}

// navigationPath maps a file URI to a project path, or to an absolute path
// marked external when it lies outside the root. A path under the root that
// does not resolve inside it (missing, or a link leading out) is dropped.
func navigationPath(root AuthorizedRoot, uri string) (string, bool, bool) {
	native, err := nativeFilePath(uri)
	if err != nil {
		return "", false, false
	}
	if rel, err := root.ResolveURI(root.Project().HostID, uri); err == nil {
		return rel, false, true
	}
	if rel, err := filepath.Rel(root.Project().Root, native); err == nil && filepath.IsLocal(rel) {
		return "", false, false
	}
	native = filepath.Clean(native)
	if !bounded(native, 4096) {
		return "", false, false
	}
	return native, true, true
}

// hoverResult reads MarkupContent, a MarkedString or an array of them. Marked
// strings are Markdown; a language-tagged one becomes a fenced code block.
func hoverResult(raw json.RawMessage) (NavigationResult, error) {
	var out NavigationResult
	if t := strings.TrimSpace(string(raw)); t == "" || t == "null" {
		return out, nil
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
		Range    *Range          `json:"range"`
	}
	if json.Unmarshal(raw, &hover) != nil {
		return out, failure("toolFailed", "The language server answer is invalid")
	}
	kind, value := "markdown", ""
	var parts []json.RawMessage
	if t := strings.TrimSpace(string(hover.Contents)); t != "" && t[0] == '[' {
		if json.Unmarshal(hover.Contents, &parts) != nil {
			return out, failure("toolFailed", "The language server answer is invalid")
		}
	} else {
		parts = []json.RawMessage{hover.Contents}
	}
	var texts []string
	for _, part := range parts {
		var text string
		if json.Unmarshal(part, &text) == nil {
			texts = append(texts, text)
			continue
		}
		var content struct {
			Kind     string `json:"kind"`
			Language string `json:"language"`
			Value    string `json:"value"`
		}
		if json.Unmarshal(part, &content) != nil {
			continue
		}
		switch {
		case content.Kind == "plaintext" && len(parts) == 1:
			kind = "plaintext"
			texts = append(texts, content.Value)
		case content.Kind != "":
			texts = append(texts, content.Value)
		default:
			texts = append(texts, fenced(content.Language, content.Value))
		}
	}
	for _, text := range texts {
		if text = strings.TrimSpace(text); text != "" {
			if value != "" {
				value += "\n\n"
			}
			value += text
		}
	}
	if value == "" {
		return out, nil
	}
	if len(value) > maxHoverBytes {
		value = value[:maxHoverBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		out.Truncated = true
	}
	out.Hover = &HoverText{Kind: kind, Value: value}
	if hover.Range != nil && validRange(hover.Range) {
		r := *hover.Range
		out.Hover.Range = &r
	}
	return out, nil
}

// fenced is a Markdown code block in language, with a fence longer than any
// backtick run in the code.
func fenced(language, code string) string {
	if !bounded(language, 64) || strings.ContainsAny(language, "` ") {
		language = ""
	}
	fence := "```"
	for strings.Contains(code, fence) {
		fence += "`"
	}
	return fence + language + "\n" + strings.TrimRight(code, "\n") + "\n" + fence
}
