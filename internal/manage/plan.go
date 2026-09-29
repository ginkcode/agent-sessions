package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// ErrPermanentNotAllowed is returned when permanent deletion was not enabled.
var ErrPermanentNotAllowed = errors.New("permanent deletion is not allowed; enable it in settings first")

// A catalog is only a hint for locating descendants; each provider must
// independently validate source paths and IDs against its configured root.
type catalogIndex struct {
	byKey    map[string]model.SessionMeta
	children map[string][]model.SessionMeta
}

func newCatalogIndex(all []model.SessionMeta) *catalogIndex {
	idx := &catalogIndex{byKey: make(map[string]model.SessionMeta, len(all)), children: make(map[string][]model.SessionMeta)}
	for _, m := range all {
		idx.byKey[m.Ref.Key()] = m
	}
	for _, m := range all {
		if m.ParentID != "" {
			parent := model.SessionRef{Agent: m.Ref.Agent, ID: m.ParentID}
			idx.children[parent.Key()] = append(idx.children[parent.Key()], m)
		}
	}
	for k := range idx.children {
		sort.Slice(idx.children[k], func(i, j int) bool { return idx.children[k][i].Ref.Key() < idx.children[k][j].Ref.Key() })
	}
	return idx
}

func (idx *catalogIndex) descendants(ref model.SessionRef) ([]model.SessionMeta, error) {
	var result []model.SessionMeta
	seen := map[string]bool{ref.Key(): true}
	var visit func(model.SessionRef) error
	visit = func(parent model.SessionRef) error {
		for _, child := range idx.children[parent.Key()] {
			key := child.Ref.Key()
			if seen[key] {
				return errors.New("manage: cycle in session graph")
			}
			seen[key] = true
			if err := visit(child.Ref); err != nil {
				return err
			}
			result = append(result, child)
		}
		return nil
	}
	if err := visit(ref); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *Manager) procLiveMap(ctx context.Context) (map[string]bool, error) {
	return newLiveGuard(m.proc).procLive(ctx)
}

// plan returns only unblocked operations, plus one display item per selected
// root. Every descendant is bound into the HMAC and included in Forgotten on
// a successful root action. Duplicate/overlapping selections are refused.
func (m *Manager) plan(ctx context.Context, selected []model.SessionRef, all []model.SessionMeta, cfg Config) ([]operation, []Item, error) {
	if len(selected) == 0 {
		return nil, nil, errors.New("manage: no sessions specified")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	idx := newCatalogIndex(all)
	procLive, procErr := m.procLiveMap(ctx)
	selectedSeen := make(map[string]bool, len(selected))
	covered := make(map[string]bool, len(selected))
	var ops []operation
	items := make([]Item, 0, len(selected))
	for _, ref := range selected {
		if ref.ID == "" || selectedSeen[ref.Key()] {
			return nil, nil, ErrPreviewStale
		}
		selectedSeen[ref.Key()] = true
		meta, ok := idx.byKey[ref.Key()]
		if !ok {
			return nil, nil, ErrPreviewStale
		}
		item := Item{Ref: ref, Agent: ref.Agent, Title: meta.Title, Action: actionFor(ref.Agent), Reversible: ref.Agent == model.AgentClaude}
		var op operation
		var err error
		if covered[ref.Key()] {
			err = errors.New("session is already covered by another selection")
		} else {
			op, err = m.planOne(ctx, meta, idx, cfg, procLive, procErr)
		}
		if err != nil {
			item.Blocked = err.Error()
		} else {
			for _, d := range op.Descendants {
				covered[d.Key()] = true
			}
			covered[ref.Key()] = true
			item = op.Item
			ops = append(ops, op)
		}
		items = append(items, item)
	}
	return ops, items, nil
}

func (m *Manager) planOne(ctx context.Context, meta model.SessionMeta, idx *catalogIndex, cfg Config, procLive map[string]bool, procErr error) (operation, error) {
	action := actionFor(meta.Ref.Agent)
	if action == "" {
		return operation{}, ErrUnsupportedAction
	}
	if action == ActionDelete && !cfg.AllowPermanentDelete {
		return operation{}, ErrPermanentNotAllowed
	}
	if action == ActionDelete && procErr != nil {
		return operation{}, fmt.Errorf("%w: process liveness unavailable", ErrLive)
	}
	if action == ActionDelete && procLive[string(meta.Ref.Agent)] {
		return operation{}, fmt.Errorf("%w: agent process is running", ErrLive)
	}
	switch meta.Ref.Agent {
	case model.AgentClaude:
		return m.planClaude(ctx, meta, idx)
	case model.AgentCodex:
		return m.planCodex(ctx, meta, idx)
	case model.AgentOpenCode:
		return m.planOpenCode(ctx, meta)
	default:
		return operation{}, ErrUnsupportedAction
	}
}

func (m *Manager) planClaude(ctx context.Context, meta model.SessionMeta, idx *catalogIndex) (operation, error) {
	if m.claude == nil {
		return operation{}, ErrUnsupportedAction
	}
	files, err := m.claude.plan(ctx, meta, m.live)
	if err != nil {
		return operation{}, err
	}
	op := operation{Item: Item{Ref: meta.Ref, Agent: meta.Ref.Agent, Title: meta.Title, Action: ActionTrash, Reversible: true}, Files: files}
	for _, f := range files {
		op.Item.Paths = append(op.Item.Paths, f.Path)
		op.Item.Bytes += bytesOf(f.Path, f.Mode)
	}
	if meta.ParentID == "" {
		children, err := m.claudeChildren(ctx, meta, idx)
		if err != nil {
			return operation{}, err
		}
		for _, child := range children {
			// A child's files live inside the parent's session directory, which
			// is already in the plan, so only liveness/safety is checked here.
			if _, err := m.claude.planChild(ctx, child, m.live); err != nil {
				return operation{}, fmt.Errorf("%w: child cannot be safely deleted", err)
			}
			op.Descendants = append(op.Descendants, child.Ref)
		}
	}
	return op, nil
}

// claudeChildren derives child IDs and paths from the real session directory,
// not merely the catalog. It checks every discovered child even if a catalog
// refresh has not indexed it yet, and detects catalog children without source.
func (m *Manager) claudeChildren(ctx context.Context, parent model.SessionMeta, idx *catalogIndex) ([]model.SessionMeta, error) {
	dir := filepath.Join(filepath.Dir(parent.SourcePath), parent.Ref.ID, "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if len(idx.children[parent.Ref.Key()]) > 0 {
				return nil, errors.New("catalog child is missing from Claude session directory")
			}
			return nil, nil
		}
		return nil, errors.New("cannot enumerate Claude children")
	}
	children := make([]model.SessionMeta, 0, len(entries))
	found := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		childID := parent.Ref.ID + "/" + strings.TrimSuffix(name, ".jsonl")
		ref := model.SessionRef{Agent: model.AgentClaude, ID: childID}
		child, ok := idx.byKey[ref.Key()]
		if !ok {
			child = model.SessionMeta{Ref: ref, ParentID: parent.Ref.ID, SourcePath: filepath.Join(dir, name)}
		}
		if child.ParentID != parent.Ref.ID || filepath.Clean(child.SourcePath) != filepath.Join(dir, name) {
			return nil, errors.New("claude child source does not match parent")
		}
		children = append(children, child)
		found[ref.Key()] = true
	}
	for _, child := range idx.children[parent.Ref.Key()] {
		if !found[child.Ref.Key()] {
			return nil, errors.New("catalog child is missing from Claude session directory")
		}
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Ref.Key() < children[j].Ref.Key() })
	return children, nil
}

func (m *Manager) planCodex(ctx context.Context, meta model.SessionMeta, idx *catalogIndex) (operation, error) {
	if m.codex == nil {
		return operation{}, ErrUnsupportedAction
	}
	children, err := idx.descendants(meta.Ref)
	if err != nil {
		return operation{}, err
	}
	// idx.descendants yields postorder: child before parent.
	all := append(append([]model.SessionMeta{}, children...), meta)
	op := operation{Item: Item{Ref: meta.Ref, Agent: meta.Ref.Agent, Title: meta.Title, Action: ActionDelete, Reversible: false}}
	for _, child := range all {
		files, err := m.codex.plan(ctx, child)
		if err != nil {
			return operation{}, fmt.Errorf("codex child or target is unsafe: %w", err)
		}
		if len(files) != 1 {
			return operation{}, errors.New("codex session has ambiguous rollout paths")
		}
		op.Files = append(op.Files, files[0])
		op.Item.Paths = append(op.Item.Paths, files[0].Path)
		op.Item.Bytes += bytesOf(files[0].Path, files[0].Mode)
		op.CLIRefs = append(op.CLIRefs, child.Ref)
		if child.Ref.Key() != meta.Ref.Key() {
			op.Descendants = append(op.Descendants, child.Ref)
		}
	}
	return op, nil
}

func (m *Manager) planOpenCode(ctx context.Context, meta model.SessionMeta) (operation, error) {
	if m.opencode == nil {
		return operation{}, ErrUnsupportedAction
	}
	members, err := m.opencode.plan(ctx, meta)
	if err != nil {
		return operation{}, err
	}
	op := operation{Item: Item{Ref: meta.Ref, Agent: meta.Ref.Agent, Title: meta.Title, Action: ActionDelete, Reversible: false, Warning: OpenCodeWarning}, OpenCodeMember: members}
	// The DB is the provider's source of truth, not a path this app exposes as
	// a directly deleted file. Paths is intentionally empty for this provider.
	for _, member := range members {
		if member.ID != meta.Ref.ID {
			op.Descendants = append(op.Descendants, model.SessionRef{Agent: model.AgentOpenCode, ID: member.ID})
		}
	}
	sort.Slice(op.Descendants, func(i, j int) bool { return op.Descendants[i].Key() < op.Descendants[j].Key() })
	return op, nil
}

// execute performs an independent fresh re-plan before each item and then
// checks process liveness once more immediately before invoking each CLI.
func (m *Manager) execute(ctx context.Context, op operation, all []model.SessionMeta, cfg Config) (Result, []model.SessionRef) {
	res := Result{Ref: op.Item.Ref, Title: op.Item.Title}
	idx := newCatalogIndex(all)
	meta, ok := idx.byKey[op.Item.Ref.Key()]
	if !ok {
		res.Error = ErrPreviewStale.Error()
		return res, nil
	}
	procLive, procErr := m.procLiveMap(ctx)
	current, err := m.planOne(ctx, meta, idx, cfg, procLive, procErr)
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	if !equalOp(op, current) {
		res.Error = ErrPreviewStale.Error()
		return res, nil
	}
	switch op.Item.Agent {
	case model.AgentClaude:
		moved, remaining, err := m.claude.trashFiles(ctx, op.Files)
		res.Moved = moved
		res.Remaining = remaining
		if err != nil {
			res.Error = err.Error()
			return res, nil
		}
		res.OK = true
		return res, append([]model.SessionRef{op.Item.Ref}, op.Descendants...)
	case model.AgentCodex:
		var forgotten []model.SessionRef
		for i, ref := range op.CLIRefs {
			if err := ctx.Err(); err != nil {
				res.Error = "deletion cancelled"
				res.Remaining = remainingPaths(op.Files, i)
				return res, forgotten
			}
			live, lerr := m.procLiveMap(ctx)
			if lerr != nil || live[string(model.AgentCodex)] {
				res.Error = ErrLive.Error()
				res.Remaining = remainingPaths(op.Files, i)
				return res, forgotten
			}
			if err := m.codex.deleteSession(ctx, ref.ID); err != nil {
				res.Error = "Codex deletion failed: " + err.Error()
				res.Remaining = remainingPaths(op.Files, i)
				return res, forgotten
			}
			forgotten = append(forgotten, ref)
		}
		res.OK = true
		return res, forgotten
	case model.AgentOpenCode:
		var v1IDs, v1OnlyIDs []string
		for _, member := range op.OpenCodeMember {
			if member.V2Target {
				live, lerr := m.procLiveMap(ctx)
				if lerr != nil || live[string(model.AgentOpenCode)] {
					res.Error = ErrLive.Error()
					return res, nil
				}
				if err := m.opencode.deleteSession(ctx, member.ID); err != nil {
					res.Error = "OpenCode 2.x deletion failed: " + err.Error()
					return res, nil
				}
			}
			if member.InV1 {
				v1IDs = append(v1IDs, member.ID)
				if !member.InV2 {
					v1OnlyIDs = append(v1OnlyIDs, member.ID)
				}
			}
		}
		if len(v1IDs) != 0 {
			live, lerr := m.procLiveMap(ctx)
			if lerr != nil || live[string(model.AgentOpenCode)] {
				res.Error = ErrLive.Error()
				return res, nil
			}
			if err := m.opencode.deleteV1Rows(ctx, v1IDs, v1OnlyIDs); err != nil {
				res.Error = "OpenCode 1.x deletion failed: " + err.Error()
				return res, nil
			}
		}
		res.OK = true
		return res, append([]model.SessionRef{op.Item.Ref}, op.Descendants...)
	default:
		res.Error = ErrUnsupportedAction.Error()
		return res, nil
	}
}

func equalOp(a, b operation) bool {
	// Ignore changing title/size, but reject path, target, action, descendant,
	// and CLI target drift. Path mode identity is checked by each fresh plan.
	return a.Item.Ref == b.Item.Ref && a.Item.Action == b.Item.Action && reflect.DeepEqual(a.Item.Paths, b.Item.Paths) && reflect.DeepEqual(a.Descendants, b.Descendants) && reflect.DeepEqual(a.CLIRefs, b.CLIRefs) && reflect.DeepEqual(a.OpenCodeMember, b.OpenCodeMember)
}

func remainingPaths(files []itemFile, start int) []string {
	paths := make([]string, 0, len(files)-start)
	for _, f := range files[start:] {
		paths = append(paths, f.Path)
	}
	return paths
}
