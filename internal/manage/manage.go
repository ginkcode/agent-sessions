// Package manage implements opt-in destructive session management.
package manage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

type Item struct {
	Ref        model.SessionRef `json:"ref"`
	Agent      model.AgentID    `json:"agent"`
	Title      string           `json:"title"`
	Paths      []string         `json:"paths"`
	Bytes      int64            `json:"bytes"`
	Reversible bool             `json:"reversible"`
	Warning    string           `json:"warning,omitempty"`
	Blocked    string           `json:"blocked,omitempty"`
	Action     Action           `json:"action"`
}
type Preview struct {
	Items      []Item `json:"items"`
	TotalBytes int64  `json:"totalBytes"`
	Token      string `json:"token"`
	TrashLabel string `json:"trashLabel,omitempty"`
}
type Result struct {
	Ref       model.SessionRef `json:"ref"`
	Title     string           `json:"title"`
	OK        bool             `json:"ok"`
	Error     string           `json:"error,omitempty"`
	Moved     []string         `json:"moved,omitempty"`
	Remaining []string         `json:"remaining,omitempty"`
	Unknown   []string         `json:"unknown,omitempty"`
}
type Report struct {
	Items      []Result           `json:"items"`
	Deleted    int                `json:"deleted"`
	Failed     int                `json:"failed"`
	FreedBytes int64              `json:"freedBytes"`
	Forgotten  []model.SessionRef `json:"forgotten"`
}

type Manager struct {
	roots          paths.Roots
	config         *ConfigStore
	claude         *claudeManager
	codex          *codexManager
	opencode       *opencodeManager
	trash          Trash
	proc           ProcFS
	live           LiveFunc
	now            NowFunc
	exec           ExecFunc
	opencodeExport OpenCodeExportFunc
	tokenKey       []byte
	// Serialize previews/deletes through this manager. This also prevents two
	// simultaneous deletes from racing over the same file and stale plan.
	mu sync.Mutex
}
type Option func(*Manager)

func WithProcFS(p ProcFS) Option      { return func(m *Manager) { m.proc = p } }
func WithLiveFunc(fn LiveFunc) Option { return func(m *Manager) { m.live = fn } }
func WithNow(fn NowFunc) Option       { return func(m *Manager) { m.now = fn } }
func WithTrash(t Trash) Option        { return func(m *Manager) { m.trash = t } }
func WithExec(fn ExecFunc) Option     { return func(m *Manager) { m.exec = fn } }

// WithOpenCodeExport replaces the OpenCode session-export command. Capture
// uses it; deletion keeps using ExecFunc.
func WithOpenCodeExport(fn OpenCodeExportFunc) Option {
	return func(m *Manager) { m.opencodeExport = fn }
}

// New does not require data roots to exist yet: provider roots are validated
// lazily so the application starts normally before any provider is installed.
func New(roots paths.Roots, configPath string, opts ...Option) (*Manager, error) {
	if configPath == "" {
		return nil, errors.New("manage: empty config path")
	}
	m := &Manager{roots: roots, config: NewConfigStore(configPath), now: defaultNow}
	for _, opt := range opts {
		opt(m)
	}
	if m.now == nil {
		m.now = defaultNow
	}
	if m.proc == nil {
		m.proc = defaultProcFS()
	}
	if m.trash == nil {
		m.trash, _ = platformTrash()
	} // Missing gio only disables Claude items.
	var err error
	if roots.Claude != "" {
		m.claude, err = newClaudeManager(roots.Claude, m.trash, m.now)
		if err != nil {
			return nil, err
		}
	}
	if roots.Codex != "" {
		m.codex, err = newCodexManager(roots.Codex, m.exec, m.now)
		if err != nil {
			return nil, err
		}
	}
	if roots.OpenCodeData != "" {
		m.opencode, err = newOpenCodeManager(roots.OpenCodeData, m.exec, m.now)
		if err != nil {
			return nil, err
		}
	}
	if m.claude == nil && m.codex == nil && m.opencode == nil {
		return nil, ErrUnsupportedAction
	}
	m.tokenKey = make([]byte, 32)
	if _, err = rand.Read(m.tokenKey); err != nil {
		return nil, fmt.Errorf("manage: random token key: %w", err)
	}
	return m, nil
}
func (m *Manager) Config() (Config, error) { return m.config.Load() }
func (m *Manager) SetConfig(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Save(cfg)
}

// UpdateConfig changes the settings on disk with fn; see ConfigStore.Update.
func (m *Manager) UpdateConfig(fn func(*Config)) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.Update(fn)
}

const tokenTTL = 5 * time.Minute

type operation struct {
	Item        Item
	Files       []itemFile
	Descendants []model.SessionRef
	// CLIRefs is ordered child-first for Codex.
	CLIRefs        []model.SessionRef
	OpenCodeMember []openCodeMember
}

// Preview accepts selected refs and a current catalog snapshot. Selected
// metadata cannot name an arbitrary deletion path: each provider re-derives
// and validates its target from the configured root before making an item.
func (m *Manager) Preview(ctx context.Context, selected []model.SessionRef, catalog []model.SessionMeta) (*Preview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	ops, items, err := m.plan(ctx, selected, catalog, cfg)
	if err != nil {
		return nil, err
	}
	var size int64
	for _, op := range ops {
		size += op.Item.Bytes
	}
	label := "Trash"
	if named, ok := m.trash.(interface{ DisplayName() string }); ok {
		label = named.DisplayName()
	}
	return &Preview{Items: items, TotalBytes: size, Token: m.seal(m.now().Add(tokenTTL), ops), TrashLabel: label}, nil
}

// Delete rebuilds the plan against fresh metadata and provider state. An
// invalid/expired HMAC, changed path, action, ref or descendant aborts before
// any destructive action. Every item is then checked again immediately before
// execution; failed items do not prevent other independent items succeeding.
func (m *Manager) Delete(ctx context.Context, selected []model.SessionRef, catalog []model.SessionMeta, token string) (*Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	expiry, mac, err := decodeToken(token)
	if err != nil || !m.now().Before(expiry) {
		return nil, ErrPreviewStale
	}
	ops, _, err := m.plan(ctx, selected, catalog, cfg)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(mac, m.mac(expiry, ops)) {
		return nil, ErrPreviewStale
	}
	report := &Report{Items: make([]Result, 0, len(ops))}
	for _, op := range ops {
		res, forgotten := m.execute(ctx, op, catalog, cfg)
		report.Items = append(report.Items, res)
		if res.OK {
			report.Deleted++
			report.FreedBytes += op.Item.Bytes
		} else {
			report.Failed++
		}
		report.Forgotten = append(report.Forgotten, forgotten...)
	}
	return report, nil
}

// The JSON encoding is an unambiguous ordered encoding, unlike joining
// user-controlled paths/IDs with delimiters. Items include paths AND the
// complete descendants/CLI targets; title/byte estimates are deliberately
// excluded so harmless catalog metadata refreshes do not stale a token.
type sealedOperation struct {
	Ref            model.SessionRef
	Action         Action
	Paths          []string
	Descendants    []model.SessionRef
	CLIRefs        []model.SessionRef
	OpenCodeMember []openCodeMember
}

func (m *Manager) mac(expiry time.Time, ops []operation) []byte {
	data := make([]sealedOperation, 0, len(ops))
	for _, op := range ops {
		data = append(data, sealedOperation{op.Item.Ref, op.Item.Action, op.Item.Paths, op.Descendants, op.CLIRefs, op.OpenCodeMember})
	}
	encoded, _ := json.Marshal(data)
	h := hmac.New(sha256.New, m.tokenKey)
	_, _ = fmt.Fprintf(h, "%d:", expiry.UnixMilli())
	_, _ = h.Write(encoded)
	return h.Sum(nil)
}

func (m *Manager) seal(expiry time.Time, ops []operation) string {
	return fmt.Sprintf("%d:%s", expiry.UnixMilli(), hex.EncodeToString(m.mac(expiry, ops)))
}

func decodeToken(token string) (time.Time, []byte, error) {
	ms, hexMAC, ok := strings.Cut(token, ":")
	if !ok || len(ms) > 20 || len(hexMAC) != sha256.Size*2 {
		return time.Time{}, nil, ErrPreviewStale
	}
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, nil, ErrPreviewStale
	}
	mac, err := hex.DecodeString(hexMAC)
	if err != nil {
		return time.Time{}, nil, ErrPreviewStale
	}
	return time.UnixMilli(n).UTC(), mac, nil
}

func actionFor(a model.AgentID) Action {
	switch a {
	case model.AgentClaude:
		return ActionTrash
	case model.AgentCodex, model.AgentOpenCode:
		return ActionDelete
	default:
		return ""
	}
}
