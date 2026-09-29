package handoff

import (
	"context"
	"slices"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// Collect finds root and all its reachable descendants of the same agent
// via ParentID, ordered root first, then descendants ordered by CreatedAt
// (ascending) then ID (ascending), guarding against cycles. Each session's
// full transcript is loaded via the provided load function.
func Collect(
	ctx context.Context,
	root model.SessionMeta,
	all []model.SessionMeta,
	load func(ctx context.Context, ref model.SessionRef) (*model.Transcript, error),
) ([]model.Transcript, error) {
	agent := root.Ref.Agent
	byParent := make(map[string][]model.SessionMeta)
	for _, m := range all {
		if m.Ref.Agent == agent && m.ParentID != "" {
			byParent[m.ParentID] = append(byParent[m.ParentID], m)
		}
	}

	visited := make(map[string]bool)
	visited[root.Ref.ID] = true

	var descendants []model.SessionMeta
	var queue []string
	queue = append(queue, root.Ref.ID)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, child := range byParent[curr] {
			if !visited[child.Ref.ID] {
				visited[child.Ref.ID] = true
				descendants = append(descendants, child)
				queue = append(queue, child.Ref.ID)
			}
		}
	}

	// Order descendants by CreatedAt ascending, then ID ascending.
	slices.SortFunc(descendants, func(a, b model.SessionMeta) int {
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.After(b.CreatedAt) {
			return 1
		}
		return strings.Compare(a.Ref.ID, b.Ref.ID)
	})

	ordered := make([]model.SessionMeta, 0, 1+len(descendants))
	ordered = append(ordered, root)
	ordered = append(ordered, descendants...)

	transcripts := make([]model.Transcript, 0, len(ordered))
	for _, meta := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, err := load(ctx, meta.Ref)
		if err != nil {
			return nil, err
		}
		if t != nil {
			transcripts = append(transcripts, *t)
		}
	}

	return transcripts, nil
}
