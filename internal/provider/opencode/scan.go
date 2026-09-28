package opencode

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// v2Checkpoint contains the entire chosen v2 ID set, including unchanged
// metadata. Later v1/legacy merging will reconcile its own IDs with this set
// before emitting removals. In particular, an incremental timestamp query is
// never an authoritative deletion sweep.
type v2Checkpoint struct {
	Version int                          `json:"version"`
	Metas   map[string]model.SessionMeta `json:"metas"`
}

// Scan implements the v2 generation only. Unsupported v1 and legacy stores
// are reported as warnings rather than accidentally treated as empty v2 data.
// Never publish a partial scan after a database or schema error.
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	result := provider.ScanResult{State: provider.ScanState{Sources: make(map[string]provider.SourceState)}}
	if err := ctx.Err(); err != nil {
		return provider.ScanResult{}, err
	}
	path := p.dbPath()
	previous := v2Checkpoint{}
	var previousIDs map[string]bool
	if source, ok := prev.Sources[path]; ok && len(source.Checkpoint) != 0 {
		if err := json.Unmarshal(source.Checkpoint, &previous); err != nil || previous.Version != 1 || previous.Metas == nil {
			// A best-effort rebuild cannot know the old ID set and could strand
			// deletions. Leave the caller's last good state intact for recovery.
			return provider.ScanResult{}, fmt.Errorf("opencode database %q: invalid v2 checkpoint; restore or reset the scan state explicitly", path)
		}
		previousIDs = make(map[string]bool, len(previous.Metas))
		for id := range previous.Metas {
			previousIDs[id] = true
		}
	} else if prev.Cursor != "" {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: cursor without v2 checkpoint; reset the scan state explicitly", path)
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if len(previous.Metas) != 0 || len(previousIDs) != 0 {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q disappeared; retaining previous sessions", path)
		}
		return result, nil
	}
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: not a regular file", path)
	}
	db, err := sqliteread.Open(ctx, path)
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	// Probes run on the single SQLite connection before any read transaction.
	session, err := sqliteread.HasTable(ctx, db, "session_v2")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	message, err := sqliteread.HasTable(ctx, db, "session_message")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	if !session || !message {
		if len(previous.Metas) != 0 || len(previousIDs) != 0 {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q lost its v2 schema; retaining previous sessions", path)
		}
		if session || message {
			result.Diag.Warn(path, 0, "incomplete OpenCode v2 schema; both session_v2 and session_message are required")
		} else {
			result.Diag.Warn(path, 0, "OpenCode v2 schema absent; v1/legacy scan is not implemented yet")
		}
		return result, nil
	}

	hasProject, err := sqliteread.HasTable(ctx, db, "project")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	full := prev.Cursor == "" || previous.Metas == nil
	var since int64
	if !full {
		since, err = strconv.ParseInt(prev.Cursor, 10, 64)
		if err != nil {
			result.Diag.Warn(path, 0, "invalid scan cursor; rebuilding from all sessions")
			full = true
		}
	}
	// A read-only transaction over the ID sweep, maximum timestamp, metadata
	// and backdated-ID lookups guarantees one consistent SQLite snapshot. With
	// sqliteread.Open's single connection, all schema probes already finished,
	// so nothing inside the transaction can block on that connection.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: begin read: %w", path, err)
	}
	metas, ids, maxUpdated, err := p.scanV2(ctx, tx, hasProject, since, full, &result.Diag)
	all := make(map[string]model.SessionMeta, len(ids))
	if err == nil {
		for _, meta := range metas {
			all[meta.Ref.ID] = meta
		}
		// New IDs can have timestamps below the saved maximum (e.g. an imported
		// session). Fetch those IDs separately instead of declaring them removed
		// or leaving a hole in the checkpoint. Batch sizes remain below SQLite
		// limits, and these queries still share the transaction snapshot.
		var missing []string
		for _, id := range ids {
			if _, ok := all[id]; ok {
				continue
			}
			if old, ok := previous.Metas[id]; ok {
				all[id] = old
			} else {
				missing = append(missing, id)
			}
		}
		for start := 0; start < len(missing); start += v2BatchSize {
			var fresh []model.SessionMeta
			end := min(start+v2BatchSize, len(missing))
			if fresh, err = p.v2Metas(ctx, tx, hasProject, 0, false, missing[start:end], &result.Diag); err != nil {
				break
			}
			for _, meta := range fresh {
				all[meta.Ref.ID] = meta
			}
		}
	}
	// End the transaction before comparing or serializing metadata. Rollback
	// releases it on errors; Commit releases it after a complete read.
	if err != nil {
		_ = tx.Rollback()
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: commit read: %w", path, err)
	}
	for _, meta := range all {
		if old, ok := previous.Metas[meta.Ref.ID]; !ok || !reflect.DeepEqual(old, meta) {
			result.Changed = append(result.Changed, meta)
		}
	}
	for id := range previousIDs {
		if _, ok := all[id]; !ok {
			result.Removed = append(result.Removed, model.SessionRef{Agent: model.AgentOpenCode, ID: id})
		}
	}
	slices.SortFunc(result.Changed, func(a, b model.SessionMeta) int { return cmp.Compare(a.Ref.Key(), b.Ref.Key()) })
	slices.SortFunc(result.Removed, func(a, b model.SessionRef) int { return cmp.Compare(a.Key(), b.Key()) })
	checkpoint, err := json.Marshal(v2Checkpoint{Version: 1, Metas: all})
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q checkpoint: %w", path, err)
	}
	if since > maxUpdated {
		maxUpdated = since
	}
	if old, err := strconv.ParseInt(prev.Cursor, 10, 64); err == nil && old > maxUpdated {
		maxUpdated = old
	}
	result.State.Cursor = strconv.FormatInt(maxUpdated, 10)
	result.State.Sources[path] = provider.SourceState{Checkpoint: checkpoint}
	return result, nil
}
