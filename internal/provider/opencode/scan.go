package opencode

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// dbCheckpoint contains the complete merged ID set. Gen records which SQLite
// generation supplied each chosen meta so a generation change is observable
// even when the metadata happens to be identical. In records every generation
// holding the ID, so a copy appearing in the other table with an old
// time_updated (as OpenCode's v1-to-v2 migration writes) is still re-merged.
type dbCheckpoint struct {
	Version int                          `json:"version"`
	Metas   map[string]model.SessionMeta `json:"metas"`
	Gen     map[string]string            `json:"gen"`
	In      map[string]string            `json:"in"`
}

const genBoth = GenV2 + "+" + GenV1

func membership(id string, v2Set, v1Set map[string]bool) string {
	switch {
	case v2Set[id] && v1Set[id]:
		return genBoth
	case v2Set[id]:
		return GenV2
	case v1Set[id]:
		return GenV1
	}
	return ""
}

// Scan reads every available SQLite generation and merges duplicate IDs. A
// v2 row wins unless the v1 row was updated strictly later.
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	result := provider.ScanResult{State: provider.ScanState{Sources: make(map[string]provider.SourceState)}}
	if err := ctx.Err(); err != nil {
		return provider.ScanResult{}, err
	}
	path := p.dbPath()
	previous, upgraded, err := decodeCheckpoint(path, prev)
	if err != nil {
		return provider.ScanResult{}, err
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if len(previous.Metas) != 0 {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q disappeared; retaining previous sessions", path)
		}
		// Detect reports the legacy generation, so say why nothing is listed.
		if legacy, legacyErr := p.hasLegacySessions(ctx); legacyErr == nil && legacy {
			result.Diag.Warn(filepath.Join(p.root, storageSubdir, legacySession), 0,
				"OpenCode legacy JSON storage is not supported yet; its sessions are not listed")
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

	// Schema probes run before the transaction because sqliteread uses one
	// connection. Every data query below then shares one read snapshot.
	hasV2Session, err := sqliteread.HasTable(ctx, db, "session_v2")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	hasV2Message, err := sqliteread.HasTable(ctx, db, "session_message")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	hasV1Session, err := sqliteread.HasTable(ctx, db, "session")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	hasV1Message, err := sqliteread.HasTable(ctx, db, "message")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	hasV1Part, err := sqliteread.HasTable(ctx, db, "part")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	v2Ready := hasV2Session && hasV2Message
	v1Ready := hasV1Session && hasV1Message && hasV1Part
	if !v2Ready && (hasV2Session || hasV2Message) {
		if checkpointHasGeneration(previous, GenV2) {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q lost its v2 schema; retaining previous sessions", path)
		}
		result.Diag.Warn(path, 0, "incomplete OpenCode v2 schema; both session_v2 and session_message are required")
	}
	if !v1Ready && (hasV1Session || hasV1Message || hasV1Part) {
		if checkpointHasGeneration(previous, GenV1) {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q lost its v1 schema; retaining previous sessions", path)
		}
		result.Diag.Warn(path, 0, "incomplete OpenCode v1 schema; session, message and part are required")
	}
	if !v2Ready && !v1Ready {
		if len(previous.Metas) != 0 {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q lost its v2 schema; retaining previous sessions", path)
		}
		if !hasV2Session && !hasV2Message && !hasV1Session && !hasV1Message && !hasV1Part {
			result.Diag.Warn(path, 0, "OpenCode SQLite session schema absent")
		}
		return result, nil
	}

	hasProject, err := sqliteread.HasTable(ctx, db, "project")
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	var v1Columns map[string]bool
	if v1Ready {
		v1Columns, err = sqliteread.Columns(ctx, db, "session")
		if err != nil {
			return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
		}
	}

	full := upgraded || prev.Cursor == "" || previous.Metas == nil
	var since int64
	if !full {
		since, err = strconv.ParseInt(prev.Cursor, 10, 64)
		if err != nil {
			result.Diag.Warn(path, 0, "invalid scan cursor; rebuilding from all sessions")
			full = true
		}
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: begin read: %w", path, err)
	}
	all, gens, in, maxUpdated, err := p.scanDBGenerations(ctx, tx, hasProject, v1Columns, v2Ready, v1Ready, since, full, previous, &result.Diag)
	if err != nil {
		_ = tx.Rollback()
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q: commit read: %w", path, err)
	}

	for id, meta := range all {
		old, existed := previous.Metas[id]
		if upgraded || !existed || previous.Gen[id] != gens[id] || !reflect.DeepEqual(old, meta) {
			result.Changed = append(result.Changed, meta)
		}
	}
	for id := range previous.Metas {
		if _, ok := all[id]; !ok {
			// A generation whose tables vanished entirely cannot prove its
			// sessions were deleted. IDs the other generation still holds
			// simply switch over; anything else is retained, not removed.
			if !v2Ready && inGeneration(previous.In[id], GenV2) {
				return provider.ScanResult{}, fmt.Errorf("opencode database %q lost its v2 schema; retaining previous sessions", path)
			}
			if !v1Ready && inGeneration(previous.In[id], GenV1) {
				return provider.ScanResult{}, fmt.Errorf("opencode database %q lost its v1 schema; retaining previous sessions", path)
			}
			result.Removed = append(result.Removed, model.SessionRef{Agent: model.AgentOpenCode, ID: id})
		}
	}
	slices.SortFunc(result.Changed, func(a, b model.SessionMeta) int { return cmp.Compare(a.Ref.Key(), b.Ref.Key()) })
	slices.SortFunc(result.Removed, func(a, b model.SessionRef) int { return cmp.Compare(a.Key(), b.Key()) })
	checkpoint, err := json.Marshal(dbCheckpoint{Version: 2, Metas: all, Gen: gens, In: in})
	if err != nil {
		return provider.ScanResult{}, fmt.Errorf("opencode database %q checkpoint: %w", path, err)
	}
	if old, parseErr := strconv.ParseInt(prev.Cursor, 10, 64); parseErr == nil && old > maxUpdated {
		maxUpdated = old
	}
	result.State.Cursor = strconv.FormatInt(maxUpdated, 10)
	result.State.Sources[path] = provider.SourceState{Checkpoint: checkpoint}
	return result, nil
}

func checkpointHasGeneration(checkpoint dbCheckpoint, generation string) bool {
	for _, value := range checkpoint.In {
		if inGeneration(value, generation) {
			return true
		}
	}
	return false
}

func inGeneration(membership, generation string) bool {
	return membership == generation || membership == genBoth
}

func decodeCheckpoint(path string, prev provider.ScanState) (dbCheckpoint, bool, error) {
	previous := dbCheckpoint{}
	source, hasSource := prev.Sources[path]
	if !hasSource || len(source.Checkpoint) == 0 {
		if prev.Cursor != "" {
			return previous, false, fmt.Errorf("opencode database %q: cursor without v2 checkpoint; reset the scan state explicitly", path)
		}
		return previous, false, nil
	}
	if err := json.Unmarshal(source.Checkpoint, &previous); err != nil {
		return dbCheckpoint{}, false, fmt.Errorf("opencode database %q: invalid v2 checkpoint; restore or reset the scan state explicitly", path)
	}
	if previous.Version == 1 {
		// Version 1 represented only v2. Keep its IDs so sessions deleted
		// before the upgrade are still removed and the retention guards still
		// apply, but force a full rescan so v1 rows are merged afresh.
		if previous.Metas == nil {
			return dbCheckpoint{}, false, fmt.Errorf("opencode database %q: invalid v2 checkpoint; restore or reset the scan state explicitly", path)
		}
		previous.Version = 2
		previous.Gen = make(map[string]string, len(previous.Metas))
		previous.In = make(map[string]string, len(previous.Metas))
		for id := range previous.Metas {
			previous.Gen[id], previous.In[id] = GenV2, GenV2
		}
		return previous, true, nil
	}
	if previous.Version != 2 || previous.Metas == nil || previous.Gen == nil || previous.In == nil {
		return dbCheckpoint{}, false, fmt.Errorf("opencode database %q: invalid v2 checkpoint; restore or reset the scan state explicitly", path)
	}
	for id := range previous.Metas {
		gen, in := previous.Gen[id], previous.In[id]
		validGen := gen == GenV2 && (in == GenV2 || in == genBoth) || gen == GenV1 && (in == GenV1 || in == genBoth)
		if !validGen {
			return dbCheckpoint{}, false, fmt.Errorf("opencode database %q: invalid v2 checkpoint; restore or reset the scan state explicitly", path)
		}
	}
	return previous, false, nil
}

func (p *Provider) scanDBGenerations(ctx context.Context, tx *sql.Tx, hasProject bool, v1Columns map[string]bool, v2Ready, v1Ready bool, since int64, full bool, previous dbCheckpoint, d *provider.Diagnostics) (map[string]model.SessionMeta, map[string]string, map[string]string, int64, error) {
	var v2List, v1List []string
	var maxUpdated int64
	var err error
	if v2Ready {
		v2List, err = v2IDs(ctx, tx)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		value, err := generationMaxUpdated(ctx, tx, "session_v2")
		if err != nil {
			return nil, nil, nil, 0, err
		}
		maxUpdated = max(maxUpdated, value)
	}
	if v1Ready {
		v1List, err = v1IDs(ctx, tx)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		value, err := generationMaxUpdated(ctx, tx, "session")
		if err != nil {
			return nil, nil, nil, 0, err
		}
		maxUpdated = max(maxUpdated, value)
	}
	v2Set, v1Set := idSet(v2List), idSet(v1List)
	union := mergeIDLists(v2List, v1List)
	selected := union
	if !full {
		changed := make(map[string]bool)
		if v2Ready {
			ids, err := generationUpdatedIDs(ctx, tx, "session_v2", since)
			if err != nil {
				return nil, nil, nil, 0, err
			}
			for _, id := range ids {
				changed[id] = true
			}
		}
		if v1Ready {
			ids, err := generationUpdatedIDs(ctx, tx, "session", since)
			if err != nil {
				return nil, nil, nil, 0, err
			}
			for _, id := range ids {
				changed[id] = true
			}
		}
		for _, id := range union {
			// A new ID, or any change in which generations hold it, re-merges
			// the ID regardless of time_updated. Both rows are then selected
			// below so the strict update-time winner is recomputed.
			if previous.In[id] != membership(id, v2Set, v1Set) {
				changed[id] = true
			}
		}
		selected = selectedMapKeys(changed)
	}

	v2Metas, v1Metas := make(map[string]model.SessionMeta), make(map[string]model.SessionMeta)
	v2Selected, v1Selected := filterIDs(selected, v2Set), filterIDs(selected, v1Set)
	if err := selectedBatches(v2Selected, func(batch []string) error {
		metas, err := p.v2Metas(ctx, tx, hasProject, 0, false, batch, d)
		if err != nil {
			return err
		}
		for _, meta := range metas {
			v2Metas[meta.Ref.ID] = meta
		}
		return nil
	}); err != nil {
		return nil, nil, nil, 0, err
	}
	if err := selectedBatches(v1Selected, func(batch []string) error {
		metas, err := p.v1Metas(ctx, tx, hasProject, v1Columns, 0, false, batch, d)
		if err != nil {
			return err
		}
		for _, meta := range metas {
			v1Metas[meta.Ref.ID] = meta
		}
		return nil
	}); err != nil {
		return nil, nil, nil, 0, err
	}

	all := make(map[string]model.SessionMeta, len(union))
	gens := make(map[string]string, len(union))
	in := make(map[string]string, len(union))
	selectedSet := idSet(selected)
	for _, id := range union {
		if !selectedSet[id] {
			old, ok := previous.Metas[id]
			if !ok {
				return nil, nil, nil, 0, fmt.Errorf("session %q missing from incremental checkpoint", id)
			}
			all[id], gens[id], in[id] = old, previous.Gen[id], previous.In[id]
			continue
		}
		v2, inV2 := v2Metas[id]
		v1, inV1 := v1Metas[id]
		if v2Set[id] && !inV2 || v1Set[id] && !inV1 {
			return nil, nil, nil, 0, fmt.Errorf("session %q disappeared while reading metadata", id)
		}
		in[id] = membership(id, v2Set, v1Set)
		// Share Load's rule and its NULL-as-0 millisecond comparison so the
		// index and the transcript always come from the same generation.
		switch pickGeneration(inV2, updatedMillis(v2.UpdatedAt), inV1, updatedMillis(v1.UpdatedAt)) {
		case GenV2:
			all[id], gens[id] = v2, GenV2
		case GenV1:
			all[id], gens[id] = v1, GenV1
		default:
			return nil, nil, nil, 0, fmt.Errorf("session %q has no readable metadata", id)
		}
	}
	return all, gens, in, maxUpdated, nil
}

// updatedMillis inverts unixMilli: a NULL time_updated (zero time) is 0, as
// sessionUpdated reports it to pickGeneration.
func updatedMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func generationMaxUpdated(ctx context.Context, db v2Query, table string) (int64, error) {
	var value sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(time_updated) FROM `+table).Scan(&value); err != nil {
		return 0, fmt.Errorf("%s maximum update time: %w", table, err)
	}
	return value.Int64, nil
}

func generationUpdatedIDs(ctx context.Context, db v2Query, table string, since int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM `+table+` WHERE time_updated >= ? ORDER BY id`, since)
	if err != nil {
		return nil, fmt.Errorf("%s updated IDs: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%s updated ID: %w", table, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s updated IDs: %w", table, err)
	}
	return ids, nil
}

func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func filterIDs(ids []string, present map[string]bool) []string {
	selected := make([]string, 0, len(ids))
	for _, id := range ids {
		if present[id] {
			selected = append(selected, id)
		}
	}
	return selected
}

func selectedMapKeys(set map[string]bool) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
