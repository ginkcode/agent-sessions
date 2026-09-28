package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

type source struct {
	Path     string // absolute path to jsonl file
	ParentID string // "" for main sessions; parent UUID for subagents
	AgentID  string // subagent id (e.g. "a1" from agent-a1.jsonl), or ""
	MetaPath string // subagents/agent-X.meta.json if present
}

// discover scans root/projects/*/*.jsonl for main sessions
// and root/projects/*/<uuid>/subagents/agent-*.jsonl for subagents.
func (p *Provider) discover(ctx context.Context) ([]source, error) {
	projRoot := filepath.Join(p.root, "projects")
	entries, err := os.ReadDir(projRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var results []source

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if !entry.IsDir() {
			continue
		}
		projDir := filepath.Join(projRoot, entry.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}

		for _, f := range files {
			name := f.Name()
			if !f.IsDir() {
				if strings.HasSuffix(name, ".jsonl") {
					results = append(results, source{
						Path:     filepath.Join(projDir, name),
						ParentID: "",
						AgentID:  "",
					})
				}
				continue
			}

			// Subdirectory might be <session-uuid>/subagents
			subDir := filepath.Join(projDir, name, "subagents")
			subFiles, err := os.ReadDir(subDir)
			if err != nil {
				continue
			}
			parentUUID := name
			for _, sf := range subFiles {
				sfName := sf.Name()
				if !sf.IsDir() && strings.HasPrefix(sfName, "agent-") && strings.HasSuffix(sfName, ".jsonl") {
					agentID := strings.TrimSuffix(strings.TrimPrefix(sfName, "agent-"), ".jsonl")
					metaPath := filepath.Join(subDir, fmt.Sprintf("agent-%s.meta.json", agentID))
					if _, err := os.Stat(metaPath); err != nil {
						metaPath = ""
					}
					results = append(results, source{
						Path:     filepath.Join(subDir, sfName),
						ParentID: parentUUID,
						AgentID:  agentID,
						MetaPath: metaPath,
					})
				}
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})

	return results, nil
}

// sourceFor locates the file source for a given SessionRef.
func (p *Provider) sourceFor(ctx context.Context, ref model.SessionRef) (source, error) {
	sources, err := p.discover(ctx)
	if err != nil {
		return source{}, err
	}

	for _, s := range sources {
		if s.ParentID == "" {
			// Main session ID is filename stem without .jsonl
			base := filepath.Base(s.Path)
			id := strings.TrimSuffix(base, ".jsonl")
			if id == ref.ID {
				return s, nil
			}
		} else {
			// Subagent ID is <parent>/agent-<agentId>
			subID := fmt.Sprintf("%s/agent-%s", s.ParentID, s.AgentID)
			if subID == ref.ID {
				return s, nil
			}
		}
	}

	return source{}, fmt.Errorf("session not found: %s", ref.Key())
}
