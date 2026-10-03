package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

var (
	claudeMainIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)
	claudeSubIDPattern  = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}/agent-[0-9a-zA-Z_-]+$`)
)

type claudeManager struct {
	root  string
	ps    *pathSafety
	trash Trash
	now   NowFunc
}

func newClaudeManager(root string, trash Trash, now NowFunc) (*claudeManager, error) {
	ps, err := newPathSafety(root)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = defaultNow
	}
	return &claudeManager{root: ps.root, ps: ps, trash: trash, now: now}, nil
}

// plan derives, validates, and checks liveness for one Claude session.
func (cm *claudeManager) plan(ctx context.Context, m model.SessionMeta, live LiveFunc) ([]itemFile, error) {
	if cm.trash == nil {
		return nil, errors.New("trash transport is unavailable on this platform")
	}
	paths, err := cm.derivePaths(ctx, m)
	if err != nil {
		return nil, safePathError(err)
	}

	files, err := filesForPaths(cm.ps, paths)
	if err != nil {
		return nil, safePathError(err)
	}

	if checker, ok := cm.trash.(TrashPathChecker); ok {
		for _, file := range files {
			if err := checker.CheckTrashPath(file.Path); err != nil {
				return nil, err
			}
		}
	}

	if err := cm.checkRecent(m, files); err != nil {
		return nil, err
	}

	if err := cm.checkLive(ctx, m, live); err != nil {
		return nil, err
	}

	return files, nil
}

// planChild validates one child for inclusion in its parent's plan. The
// child's own files are returned so they are trashed before the parent's
// session directory, and its recency/live state gates the parent.
func (cm *claudeManager) planChild(ctx context.Context, child model.SessionMeta, live LiveFunc) ([]itemFile, error) {
	files, err := cm.plan(ctx, child, live)
	if err != nil {
		return nil, err
	}
	// A child's own optional directory (agent-<id>/) is not part of the
	// Claude layout, so its plan is files only. Refuse surprises.
	for _, f := range files {
		if f.Mode.IsDir() {
			return nil, errors.New("unexpected directory in child session")
		}
	}
	return files, nil
}

func (cm *claudeManager) derivePaths(ctx context.Context, m model.SessionMeta) ([]string, error) {
	if m.Ref.Agent != model.AgentClaude {
		return nil, ErrUnsupportedAction
	}
	if m.SourcePath == "" {
		return nil, errors.New("missing session path")
	}

	cleanedSource := filepath.Clean(m.SourcePath)
	if _, err := cm.ps.classify(cleanedSource); err != nil {
		return nil, err
	}

	var candidates []string
	if m.ParentID == "" {
		// Main session
		if !claudeMainIDPattern.MatchString(m.Ref.ID) {
			return nil, errors.New("invalid session ID format")
		}
		base := filepath.Base(cleanedSource)
		if base != m.Ref.ID+".jsonl" {
			return nil, errors.New("session path does not match session ID")
		}
		candidates = append(candidates, cleanedSource)

		// Sibling directory <parentDir>/<uuid>
		parentDir := filepath.Dir(cleanedSource)
		sessionDir := filepath.Join(parentDir, m.Ref.ID)
		if fi, err := os.Lstat(sessionDir); err == nil && fi.IsDir() {
			// WalkDir the whole session directory to ensure no protected files or symlinks exist
			err := cm.ps.checkTree(func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					return nil
				}
			}, sessionDir)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, sessionDir)
		}
	} else {
		// Subagent child session
		if !claudeSubIDPattern.MatchString(m.Ref.ID) {
			return nil, errors.New("invalid subagent session ID format")
		}
		parentDir := filepath.Dir(cleanedSource)
		if filepath.Base(parentDir) != "subagents" || filepath.Base(filepath.Dir(parentDir)) != m.ParentID {
			return nil, errors.New("subagent path does not match parent ID")
		}
		base := filepath.Base(cleanedSource)
		if base != filepath.Base(m.Ref.ID)+".jsonl" {
			return nil, errors.New("subagent path does not match session ID")
		}
		candidates = append(candidates, cleanedSource)

		aid := strings.TrimSuffix(strings.TrimPrefix(base, "agent-"), ".jsonl")
		metaPath := filepath.Join(parentDir, fmt.Sprintf("agent-%s.meta.json", aid))
		if _, err := os.Lstat(metaPath); err == nil {
			if _, err := cm.ps.classify(metaPath); err != nil {
				return nil, err
			}
			candidates = append(candidates, metaPath)
		}
	}

	sortClaudePaths(candidates)
	return candidates, nil
}

func (cm *claudeManager) checkRecent(m model.SessionMeta, files []itemFile) error {
	now := cm.now()
	for _, f := range files {
		fi, err := os.Lstat(f.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return errors.New("cannot inspect session timestamp")
		}
		if now.Sub(fi.ModTime()) < recentWindow {
			return fmt.Errorf("%w: session was active within 10 minutes", ErrLive)
		}
	}
	if !m.UpdatedAt.IsZero() && now.Sub(m.UpdatedAt) < recentWindow {
		return fmt.Errorf("%w: session was active within 10 minutes", ErrLive)
	}
	return nil
}

func (cm *claudeManager) checkLive(ctx context.Context, m model.SessionMeta, live LiveFunc) error {
	if live == nil {
		// As required: detector absent for Claude must fail closed
		return fmt.Errorf("%w: liveness detector unavailable", ErrLive)
	}
	isLive, err := live(ctx, string(model.AgentClaude), m.Ref.ID)
	if err != nil {
		return fmt.Errorf("%w: liveness check failed", ErrLive)
	}
	if isLive {
		return fmt.Errorf("%w: session is currently active", ErrLive)
	}
	return nil
}

// trash executes trash operation file-by-file with cancellation check.
func (cm *claudeManager) trashFiles(ctx context.Context, files []itemFile, beforeTrash func() error) (moved []string, remaining []string, err error) {
	if cm.trash == nil {
		for _, f := range files {
			remaining = append(remaining, f.Path)
		}
		return nil, remaining, errors.New("trash is not supported on this platform")
	}

	for i, f := range files {
		select {
		case <-ctx.Done():
			for j := i; j < len(files); j++ {
				remaining = append(remaining, files[j].Path)
			}
			return moved, remaining, ctx.Err()
		default:
		}

		if beforeTrash != nil {
			if err := beforeTrash(); err != nil {
				return moved, remainingPaths(files, i), err
			}
		}
		if trErr := cm.trash.Trash(f.Path); trErr != nil {
			if errors.Is(trErr, ErrTrashOutcomeUnknown) {
				// The shell may have moved all or part of the current target.
				// Do not list it as remaining or automatically retry it.
				return moved, remainingPaths(files, i+1), ErrTrashOutcomeUnknown
			}
			return moved, remainingPaths(files, i), errors.New("trash operation failed")
		}
		moved = append(moved, f.Path)
	}
	return moved, nil, nil
}

// sortClaudePaths ensures files inside a directory are trashed before the directory itself,
// while preserving discovery order (e.g. main session jsonl before session directory).
func sortClaudePaths(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		pi, pj := paths[i], paths[j]
		if strings.HasPrefix(pi, pj+string(filepath.Separator)) {
			return true
		}
		if strings.HasPrefix(pj, pi+string(filepath.Separator)) {
			return false
		}
		return false
	})
}
