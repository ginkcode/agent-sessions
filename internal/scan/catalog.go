// Package scan coordinates providers and keeps their session metadata in memory.
package scan

import (
	"slices"
	"strings"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Catalog is a concurrency-safe snapshot of the sessions found by providers.
type Catalog struct {
	mu    sync.RWMutex
	byKey map[string]model.SessionMeta
}

func NewCatalog() *Catalog {
	return &Catalog{byKey: make(map[string]model.SessionMeta)}
}

// Apply removes old sessions before upserting changed sessions.
func (c *Catalog) Apply(result provider.ScanResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apply(result)
}

func (c *Catalog) apply(result provider.ScanResult) {
	if c.byKey == nil {
		c.byKey = make(map[string]model.SessionMeta)
	}
	for _, ref := range result.Removed {
		delete(c.byKey, ref.Key())
	}
	for _, m := range result.Changed {
		c.byKey[m.Ref.Key()] = m
	}
}

// applyLive makes the scan result and its live overlay visible atomically.
// A failed live call keeps the badges from the previous scan, even for
// sessions the scan just changed; a successful call re-derives every badge of
// this provider from the live map, including for unchanged sessions.
func (c *Catalog) applyLive(id model.AgentID, result provider.ScanResult, live map[string]provider.LiveInfo, liveErr error, hasLive bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var badges map[string]provider.LiveInfo // session ID -> last known badge
	if liveErr != nil && hasLive {
		badges = make(map[string]provider.LiveInfo)
		for _, m := range c.byKey {
			if m.Ref.Agent == id && m.Live {
				badges[m.Ref.ID] = provider.LiveInfo{Status: m.LiveStatus}
			}
		}
	}
	c.apply(result)
	if !hasLive {
		return
	}
	if liveErr != nil {
		// Overlay with the stale badges instead of erasing them.
		live = badges
	}
	for key, m := range c.byKey {
		if m.Ref.Agent != id {
			continue
		}
		info, ok := live[m.Ref.ID]
		m.Live = ok
		m.LiveStatus = ""
		if ok {
			m.LiveStatus = info.Status
		}
		c.byKey[key] = m
	}
}

// All returns a sorted, independent snapshot of every session.
func (c *Catalog) All() []model.SessionMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]model.SessionMeta, 0, len(c.byKey))
	for _, m := range c.byKey {
		out = append(out, m)
	}
	sortSessions(out)
	return out
}

// Get returns the metadata for one session, if it exists.
func (c *Catalog) Get(ref model.SessionRef) (model.SessionMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byKey[ref.Key()]
	return m, ok
}

// Children returns sessions whose parent belongs to the same provider.
func (c *Catalog) Children(ref model.SessionRef) []model.SessionMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]model.SessionMeta, 0)
	for _, m := range c.byKey {
		if m.Ref.Agent == ref.Agent && m.ParentID == ref.ID {
			out = append(out, m)
		}
	}
	sortSessions(out)
	return out
}

func sortSessions(sessions []model.SessionMeta) {
	slices.SortFunc(sessions, func(a, b model.SessionMeta) int {
		if a.UpdatedAt.After(b.UpdatedAt) {
			return -1
		}
		if a.UpdatedAt.Before(b.UpdatedAt) {
			return 1
		}
		return strings.Compare(a.Ref.Key(), b.Ref.Key())
	})
}
