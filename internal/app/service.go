package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/group"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// transcriptLRUCapacity bounds how many full transcripts stay resident.
const transcriptLRUCapacity = 4

// maxBlobTextBytes caps blob payloads converted to UTF-8 text.
const maxBlobTextBytes = 4 << 20

// ErrUnknownSession is returned when a session reference is not in the catalog.
var ErrUnknownSession = errors.New("unknown session")

// ErrUnknownProvider is returned when no provider is registered for an agent.
var ErrUnknownProvider = errors.New("unknown provider")

// ErrUnknownGroup is returned when a group key does not match any group.
var ErrUnknownGroup = errors.New("unknown group")

// Service is the bound application service exposed to the desktop frontend.
// It owns the session catalog, the provider set, and the transcript LRU.
// (The embedded Wails runtime context lives on App; see app.go.)
type Service struct {
	catalog   *scan.Catalog
	providers provider.Set

	// diagMu guards the scan-bookkeeping maps: the asynchronous bootstrap
	// goroutine writes them while bound calls read them concurrently.
	diagMu   sync.RWMutex
	diag     map[model.AgentID]provider.Diagnostics
	scanErrs map[model.AgentID]error
	states   map[model.AgentID]provider.ScanState

	transcripts *lru[string, *model.Transcript]
}

// NewService wires a Service onto an existing catalog and provider set.
func NewService(catalog *scan.Catalog, providers provider.Set) *Service {
	if catalog == nil {
		catalog = scan.NewCatalog()
	}
	return &Service{
		catalog:     catalog,
		providers:   providers,
		diag:        make(map[model.AgentID]provider.Diagnostics),
		scanErrs:    make(map[model.AgentID]error),
		states:      make(map[model.AgentID]provider.ScanState),
		transcripts: newLRU[string, *model.Transcript](transcriptLRUCapacity),
	}
}

// ApplyReport records scan diagnostics, errors, and resume states from the
// latest scan run so the frontend can display provider health.
func (s *Service) ApplyReport(report scan.Report) {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	for id, d := range report.Diag {
		s.diag[id] = d
	}
	for id, err := range report.Errors {
		s.scanErrs[id] = err
	}
	for id, st := range report.States {
		s.states[id] = st
	}
}

// ListGroups returns the grouped navigation tree for the filtered snapshot.
// Filtering happens before grouping so node counts reflect the filter.
func (s *Service) ListGroups(mode GroupMode, filter FilterOpts) ([]GroupNode, error) {
	sessions := filterSessions(s.catalog.All(), filter)
	return buildGroupNodes(sessions, mode), nil
}

// ListSessions returns session metadata for one group (or every session when
// groupKey is empty), filtered and sorted for the session list pane.
func (s *Service) ListSessions(groupKey string, filter FilterOpts, sortOpts SortOpts) ([]model.SessionMeta, error) {
	sessions := filterSessions(s.catalog.All(), filter)
	if groupKey == "" {
		sortSessions(sessions, sortOpts)
		return sessions, nil
	}
	refs, ok := groupSessionRefs(groupKey, sessions)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownGroup, groupKey)
	}
	byKey := make(map[string]model.SessionMeta, len(refs))
	for _, m := range sessions {
		byKey[m.Ref.Key()] = m
	}
	out := make([]model.SessionMeta, 0, len(refs))
	for _, ref := range refs {
		if m, ok := byKey[ref.Key()]; ok {
			out = append(out, m)
		}
	}
	sortSessions(out, sortOpts)
	return out, nil
}

// GetSessionMeta returns the catalog metadata for one session.
func (s *Service) GetSessionMeta(ref model.SessionRef) (model.SessionMeta, error) {
	m, ok := s.catalog.Get(ref)
	if !ok {
		return model.SessionMeta{}, fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
	}
	return m, nil
}

// GetMessages returns one page of a session transcript. The whole transcript
// is loaded once (on cache miss) and cached in the LRU; paging is a slice of
// the cached messages.
func (s *Service) GetMessages(ctx context.Context, ref model.SessionRef, offset int, limit int) (MessagesPage, error) {
	if offset < 0 || limit <= 0 {
		return MessagesPage{}, fmt.Errorf("invalid page offset=%d limit=%d", offset, limit)
	}
	all, err := s.loadTranscript(ctx, ref)
	if err != nil {
		return MessagesPage{}, err
	}
	total := len(all.Messages)
	start := min(offset, total)
	end := min(start+limit, total)
	page := make([]model.Message, end-start)
	copy(page, all.Messages[start:end])
	return MessagesPage{
		Messages:   page,
		Offset:     offset,
		Limit:      limit,
		TotalCount: total,
		HasMore:    end < total,
	}, nil
}

// GetBlob returns provider-attached content referenced by a message part.
// UTF-8 payloads are returned as text; anything else is base64-encoded with
// IsBinary set so the UI can decide how to present it.
func (s *Service) GetBlob(ctx context.Context, ref model.SessionRef, key string) (BlobResponse, error) {
	prov, ok := s.providers.Get(ref.Agent)
	if !ok {
		return BlobResponse{}, fmt.Errorf("%w: %s", ErrUnknownProvider, ref.Agent)
	}
	raw, err := prov.Blob(ctx, ref, key)
	if err != nil {
		return BlobResponse{}, err
	}
	resp := BlobResponse{Mime: "application/octet-stream"}
	if len(raw) <= maxBlobTextBytes && utf8.Valid(raw) && !looksBinary(raw) {
		resp.Data = string(raw)
		resp.Mime = "text/plain"
		return resp, nil
	}
	resp.Data = base64.StdEncoding.EncodeToString(raw)
	resp.IsBinary = true
	return resp, nil
}

func looksBinary(data []byte) bool {
	n := len(data)
	if n > 1024 {
		n = 1024
	}
	return bytes.IndexByte(data[:n], 0) != -1
}

// CopyResumeCommand builds the provider resume command for a session. It
// returns the formatted command line; pasting to the clipboard is the
// frontend's job in this milestone (runtime clipboard needs the Wails
// context, which is wired up with App in app.go).
func (s *Service) CopyResumeCommand(ref model.SessionRef) (string, error) {
	prov, ok := s.providers.Get(ref.Agent)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownProvider, ref.Agent)
	}
	m, ok := s.catalog.Get(ref)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
	}
	return formatResumeCommand(prov.ResumeCommand(m), m.CWD), nil
}

// RevealSource exposes the session's source path to the frontend. Actually
// spawning a file manager is deliberately deferred to the desktop wiring in
// app.go; returning the path keeps the service read-only and testable.
func (s *Service) RevealSource(ref model.SessionRef) (string, error) {
	m, ok := s.catalog.Get(ref)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
	}
	if m.SourcePath == "" {
		return "", fmt.Errorf("session %s has no source path", ref.Key())
	}
	return filepath.Dir(m.SourcePath), nil
}

// Diagnostics returns aggregated scanner health, merging per-provider
// diagnostics and scan errors into one snapshot.
func (s *Service) Diagnostics() (provider.Diagnostics, error) {
	s.diagMu.RLock()
	defer s.diagMu.RUnlock()

	var total provider.Diagnostics
	for _, d := range s.diag {
		total.Merge(d)
	}
	return total, nil
}

// DiagnosticsByProvider returns per-provider diagnostics and errors.
func (s *Service) DiagnosticsByProvider() map[string]provider.Diagnostics {
	s.diagMu.RLock()
	defer s.diagMu.RUnlock()

	out := make(map[string]provider.Diagnostics, len(s.diag))
	for id, d := range s.diag {
		out[string(id)] = d
	}
	return out
}

// ScanErrors returns the last scan error per provider, if any.
func (s *Service) ScanErrors() map[string]string {
	s.diagMu.RLock()
	defer s.diagMu.RUnlock()

	out := make(map[string]string, len(s.scanErrs))
	for id, err := range s.scanErrs {
		out[string(id)] = err.Error()
	}
	return out
}

func (s *Service) loadTranscript(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	key := ref.Key()
	if tr, ok := s.transcripts.get(key); ok {
		return tr, nil
	}
	prov, ok := s.providers.Get(ref.Agent)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, ref.Agent)
	}
	tr, err := prov.Load(ctx, ref)
	if err != nil {
		return nil, err
	}
	s.transcripts.put(key, tr)
	return tr, nil
}

// filterSessions applies the FilterOpts to a metadata snapshot.
func filterSessions(sessions []model.SessionMeta, f FilterOpts) []model.SessionMeta {
	query := strings.ToLower(strings.TrimSpace(f.Query))
	agent := model.AgentID(f.Agent)
	out := make([]model.SessionMeta, 0, len(sessions))
	for _, m := range sessions {
		if agent != "" && m.Ref.Agent != agent {
			continue
		}
		if f.LiveOnly && !m.Live {
			continue
		}
		if !f.Archived && m.Archived {
			continue
		}
		if query != "" && !matchesQuery(m, query) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// matchesQuery does a substring match over the searchable text fields.
func matchesQuery(m model.SessionMeta, query string) bool {
	haystacks := []string{
		m.Title,
		m.FirstPrompt,
		m.CWD,
		m.RepoRoot,
		m.Model,
		m.GitBranch,
		string(m.Ref.Agent),
		m.Ref.ID,
	}
	for _, h := range haystacks {
		if h != "" && strings.Contains(strings.ToLower(h), query) {
			return true
		}
	}
	return false
}

// groupSessionRefs resolves a group key against a filtered snapshot by
// rebuilding the group tree and reading the matched node's session refs.
func groupSessionRefs(groupKey string, sessions []model.SessionMeta) ([]model.SessionRef, bool) {
	for _, mode := range []GroupMode{GroupModeDirAgent, GroupModeAgentDir, GroupModeFlat} {
		nodes := buildGroupNodes(sessions, mode)
		for _, node := range flattenGroupNodes(nodes) {
			if node.Key == groupKey && len(node.Sessions) > 0 {
				return node.Sessions, true
			}
		}
	}
	return nil, false
}

func flattenGroupNodes(nodes []GroupNode) []GroupNode {
	out := make([]GroupNode, 0, len(nodes))
	var walk func([]GroupNode)
	walk = func(ns []GroupNode) {
		for _, n := range ns {
			out = append(out, n)
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// buildGroupNodes converts a session snapshot into the planned GroupNode tree
// via the shared grouping engine. Session leaves are not returned as top
// level entries; groups carry their session refs on the node.
func buildGroupNodes(sessions []model.SessionMeta, mode GroupMode) []GroupNode {
	groupMode := group.GroupMode(mode)
	switch mode {
	case GroupModeDirAgent, GroupModeAgentDir, GroupModeFlat:
	default:
		groupMode = group.DirAgent
	}
	nodes := group.Build(sessions, group.Options{Mode: groupMode})
	out := make([]GroupNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, convertGroupNode(n))
	}
	return out
}

func convertGroupNode(n group.Node) GroupNode {
	out := GroupNode{
		Key:          n.Key,
		Label:        n.Label,
		Agent:        string(n.Agent),
		CWD:          n.Path,
		CWDMissing:   n.Missing,
		SessionCount: n.Count,
	}
	// Group (non-session) nodes list the sessions they contain so the UI can
	// resolve ListSessions(groupKey) without re-deriving grouping client-side.
	if n.Kind != group.Session {
		out.Sessions = n.SessionRefs
		out.Secondary = n.Label
	}
	if len(n.Children) > 0 {
		out.Children = make([]GroupNode, 0, len(n.Children))
		for _, child := range n.Children {
			out.Children = append(out.Children, convertGroupNode(child))
		}
	}
	return out
}

// sortSessions orders session metadata for the list pane. Unknown fields fall
// back to the catalog's recency ordering semantics.
func sortSessions(sessions []model.SessionMeta, opts SortOpts) {
	desc := opts.Desc
	less := func(a, b model.SessionMeta) bool { return false }
	switch opts.Field {
	case "created", "createdAt":
		less = func(a, b model.SessionMeta) bool { return a.CreatedAt.Before(b.CreatedAt) }
	case "title":
		less = func(a, b model.SessionMeta) bool { return strings.ToLower(a.Title) < strings.ToLower(b.Title) }
	case "messages", "count":
		less = func(a, b model.SessionMeta) bool { return a.Counts.Total() < b.Counts.Total() }
	case "tokens":
		less = func(a, b model.SessionMeta) bool {
			return a.Tokens.Input+a.Tokens.Output < b.Tokens.Input+b.Tokens.Output
		}
	case "cost", "costUsd":
		less = func(a, b model.SessionMeta) bool { return a.CostUSD < b.CostUSD }
	default: // "updated", "updatedAt"
		less = func(a, b model.SessionMeta) bool { return a.UpdatedAt.Before(b.UpdatedAt) }
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if desc {
			return less(sessions[j], sessions[i])
		}
		return less(sessions[i], sessions[j])
	})
}
