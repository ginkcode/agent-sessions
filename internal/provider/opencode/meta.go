package opencode

import (
	"database/sql"
	"math"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

type metaOrigin struct {
	projectID string
	worktree  sql.NullString
}

// finishMetas applies the generation-independent metadata rules after message
// counts have supplied FirstPrompt and, for untitled sessions, the latest
// user prompt with text as Title. origins is keyed by the session ID.
func (p *Provider) finishMetas(metas []model.SessionMeta, origins map[string]metaOrigin, d *provider.Diagnostics) {
	for i := range metas {
		meta := &metas[i]
		origin := origins[meta.Ref.ID]
		meta.CWD = pathutil.NormalizeDir(meta.CWD)
		meta.CWDMissing = meta.CWD != "" && !pathutil.Exists(meta.CWD)
		if math.IsInf(meta.CostUSD, 0) || math.IsNaN(meta.CostUSD) {
			// SQLite accepts values such as 9e999. Non-finite floats cannot be
			// represented in the JSON checkpoint.
			d.Warn(p.dbPath(), 0, "session %q has a non-finite cost", meta.Ref.ID)
			meta.CostUSD = 0
		}
		if origin.projectID != "global" && origin.worktree.Valid && origin.worktree.String != "/" && pathutil.IsAbs(origin.worktree.String) {
			meta.RepoRoot = pathutil.NormalizeDir(origin.worktree.String)
		} else if repo, ok := p.git.Resolve(meta.CWD); ok {
			meta.RepoRoot = repo.MainRoot
		}
		if meta.Title == "" {
			meta.Title = meta.FirstPrompt
			if meta.Title == "" {
				meta.Title = "(untitled)"
			}
		}
	}
}
