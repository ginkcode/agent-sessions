package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/group"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
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

// Service is the core application service. It owns the session catalog, the
// provider set, and the transcript/bundle LRUs. It has no Wails or GUI dependencies.
type Service struct {
	catalog   *scan.Catalog
	providers provider.Set

	diagMu   sync.RWMutex
	diag     map[model.AgentID]provider.Diagnostics
	scanErrs map[model.AgentID]error
	states   map[model.AgentID]provider.ScanState

	transcripts *LRU[string, *model.Transcript]
	// transcriptGen counts evictions, so a load that raced one is not cached.
	transcriptGen atomic.Uint64
	bundles       *LRU[string, *bundle.Bundle]
	dataDir       string
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
		transcripts: NewLRU[string, *model.Transcript](transcriptLRUCapacity),
		bundles:     NewLRU[string, *bundle.Bundle](8),
	}
}

// Catalog returns the underlying session catalog.
func (s *Service) Catalog() *scan.Catalog {
	return s.catalog
}

// Providers returns the underlying provider set.
func (s *Service) Providers() provider.Set {
	return s.providers
}

// DataDir returns the base directory for handoffs and app data.
func (s *Service) DataDir() string {
	if s.dataDir != "" {
		return s.dataDir
	}
	roots, err := paths.Default()
	if err == nil && roots.Data != "" {
		return roots.Data
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "agent-sessions")
}

// SetDataDir overrides the data directory used for handoff context files.
func (s *Service) SetDataDir(dir string) {
	s.dataDir = dir
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
func (s *Service) ListGroups(mode GroupMode, filter FilterOpts) ([]GroupNode, error) {
	sessions := filterSessions(s.catalog.All(), filter)
	return buildGroupNodes(sessions, mode), nil
}

// AgentCounts returns the number of sessions per agent across the whole
// catalog. The agent filter is ignored so every agent's total stays visible
// while one agent is selected; every other filter applies.
func (s *Service) AgentCounts(filter FilterOpts) (map[string]int, error) {
	filter.Agent = ""
	counts := make(map[string]int)
	for _, m := range filterSessions(s.catalog.All(), filter) {
		counts[string(m.Ref.Agent)]++
	}
	return counts, nil
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

// GetMessages returns one page of a session transcript.
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

// CopyResumeCommand builds the provider resume command for a session.
func (s *Service) CopyResumeCommand(ref model.SessionRef) (string, error) {
	prov, ok := s.providers.Get(ref.Agent)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownProvider, ref.Agent)
	}
	m, ok := s.catalog.Get(ref)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
	}
	return FormatResumeCommand(prov.ResumeCommand(m), m.CWD), nil
}

// RevealSource exposes the session's source path directory.
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

// Diagnostics returns aggregated scanner health.
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

// ScanStates returns a snapshot of provider scan states for incremental runners.
func (s *Service) ScanStates() map[model.AgentID]provider.ScanState {
	s.diagMu.RLock()
	defer s.diagMu.RUnlock()

	out := make(map[model.AgentID]provider.ScanState, len(s.states))
	for id, st := range s.states {
		out[id] = st
	}
	return out
}

func (s *Service) loadTranscript(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	key := ref.Key()
	if tr, ok := s.transcripts.Get(key); ok {
		return tr, nil
	}
	prov, ok := s.providers.Get(ref.Agent)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, ref.Agent)
	}
	gen := s.transcriptGen.Load()
	tr, err := prov.Load(ctx, ref)
	if err != nil {
		return nil, err
	}
	// A session that changed during the load may have been read before the
	// change; serve it once but do not cache it.
	if s.transcriptGen.Load() == gen {
		s.transcripts.Put(key, tr)
	}
	return tr, nil
}

func (s *Service) EvictTranscript(key string) {
	s.transcriptGen.Add(1)
	s.transcripts.Evict(key)
}

// EvictTranscripts drops the cached transcripts of sessions a scan reported
// changed or removed, so the next read loads their current content.
func (s *Service) EvictTranscripts(refs ...[]model.SessionRef) {
	s.transcriptGen.Add(1)
	for _, list := range refs {
		for _, ref := range list {
			s.transcripts.Evict(ref.Key())
		}
	}
}

// ClearTranscripts drops every cached transcript, for scans that rebuild the
// catalog without reporting which sessions changed.
func (s *Service) ClearTranscripts() {
	s.transcriptGen.Add(1)
	s.transcripts.Clear()
}

func (s *Service) evictTranscript(key string) {
	s.EvictTranscript(key)
}

// ValidateRefs checks that refs still exist in the current catalog.
func (s *Service) ValidateRefs(refs []model.SessionRef) error {
	for _, ref := range refs {
		if _, ok := s.catalog.Get(ref); !ok {
			return fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
		}
	}
	return nil
}

func (s *Service) validateRefs(refs []model.SessionRef) error {
	return s.ValidateRefs(refs)
}

func filterSessions(sessions []model.SessionMeta, f FilterOpts) []model.SessionMeta {
	query := strings.ToLower(strings.TrimSpace(f.Query))
	path := strings.ToLower(strings.TrimSpace(f.Path))
	agent := model.AgentID(f.Agent)
	var hasChild map[string]bool
	if f.HasSubagents {
		hasChild = make(map[string]bool)
		for _, m := range sessions {
			if m.ParentID != "" {
				hasChild[model.SessionRef{Agent: m.Ref.Agent, ID: m.ParentID}.Key()] = true
			}
		}
	}
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
		if path != "" && !matchesPath(m, path) {
			continue
		}
		if f.HasSubagents && !hasChild[m.Ref.Key()] {
			continue
		}
		out = append(out, m)
	}
	return out
}

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

func matchesPath(m model.SessionMeta, path string) bool {
	return strings.Contains(strings.ToLower(m.CWD), path) ||
		strings.Contains(strings.ToLower(m.RepoRoot), path)
}

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

// FlattenGroupNodes recursively walks a tree of group nodes into a flat slice.
func FlattenGroupNodes(nodes []GroupNode) []GroupNode {
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

func flattenGroupNodes(nodes []GroupNode) []GroupNode {
	return FlattenGroupNodes(nodes)
}

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
		Kind:         string(n.Kind),
		Agent:        string(n.Agent),
		CWD:          n.Path,
		CWDMissing:   n.Missing,
		SessionCount: n.Count,
	}
	if n.Kind != group.Session {
		out.Sessions = n.SessionRefs
		out.Secondary = n.Label
	} else {
		out.Sessions = sessionSubtreeRefs(n)
	}
	if len(n.Children) > 0 {
		out.Children = make([]GroupNode, 0, len(n.Children))
		for _, child := range n.Children {
			out.Children = append(out.Children, convertGroupNode(child))
		}
	}
	return out
}

func sessionSubtreeRefs(n group.Node) []model.SessionRef {
	refs := append([]model.SessionRef(nil), n.SessionRefs...)
	for _, child := range n.Children {
		if child.Kind == group.Session {
			refs = append(refs, sessionSubtreeRefs(child)...)
		}
	}
	return refs
}

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
	default:
		less = func(a, b model.SessionMeta) bool { return a.UpdatedAt.Before(b.UpdatedAt) }
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if desc {
			return less(sessions[j], sessions[i])
		}
		return less(sessions[i], sessions[j])
	})
}
