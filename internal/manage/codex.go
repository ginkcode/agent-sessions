package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// uuidPattern is intentionally strict: `codex delete` accepts only UUID
// session IDs. A name or option-looking argument never reaches the CLI.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ExecFunc runs a CLI command with an explicit environment and working dir.
// argv[0] is the executable name and argv[1:] its args.
type ExecFunc func(ctx context.Context, argv []string, dir string, env []string) error

// defaultExec invokes a command without a shell. Combined output is captured
// and reduced to a generic failure: provider CLI output can embed absolute
// paths, user names, and API keys, so it is never echoed back.
func defaultExec(ctx context.Context, argv []string, dir string, env []string) error {
	if len(argv) == 0 {
		return errors.New("manage: empty command")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := runStandalone(ctx, argv, dir, env); err != nil {
		return errors.New("manage: provider command failed")
	}
	return nil
}

// codexManager handles permanent deletion via the codex CLI.
type codexManager struct {
	root string
	ps   *pathSafety
	exec ExecFunc
	now  NowFunc
}

func newCodexManager(root string, execFn ExecFunc, now NowFunc) (*codexManager, error) {
	ps, err := newPathSafety(root)
	if err != nil {
		return nil, err
	}
	if execFn == nil {
		execFn = defaultExec
	}
	if now == nil {
		now = defaultNow
	}
	return &codexManager{root: ps.root, ps: ps, exec: execFn, now: now}, nil
}

// plan derives, validates, and checks liveness for one Codex rollout.
func (cm *codexManager) plan(ctx context.Context, m model.SessionMeta) ([]itemFile, error) {
	paths, err := cm.derivePaths(ctx, m)
	if err != nil {
		return nil, safePathError(err)
	}

	files, err := filesForPaths(cm.ps, paths)
	if err != nil {
		return nil, safePathError(err)
	}

	if err := cm.checkRecent(m, files); err != nil {
		return nil, err
	}
	return files, nil
}

// derivePaths rebuilds the rollout location from the Codex root and the
// session ID rather than trusting a caller-supplied path.
func (cm *codexManager) derivePaths(ctx context.Context, m model.SessionMeta) ([]string, error) {
	if m.Ref.Agent != model.AgentCodex {
		return nil, ErrUnsupportedAction
	}
	if !uuidPattern.MatchString(m.Ref.ID) {
		return nil, errors.New("invalid codex session ID format")
	}

	// Discover under sessions/YYYY/MM/DD and archived_sessions; both hold
	// rollout-<id>.jsonl files the codex CLI owns.
	var matches []string
	for _, sub := range []string{"sessions", "archived_sessions"} {
		base := filepath.Join(cm.root, sub)
		if _, err := os.Lstat(base); err != nil {
			continue // not present in this install
		}
		err := cm.walkRollouts(ctx, base, m.Ref.ID, &matches)
		if err != nil {
			return nil, err
		}
	}
	if len(matches) == 0 {
		return nil, errors.New("codex rollout not found under provider root")
	}
	return matches, nil
}

func (cm *codexManager) walkRollouts(ctx context.Context, dir, id string, matches *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A missing directory is expected; any other read failure would
		// leave rollout paths unknown, so it must not be swallowed.
		return err
	}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if entry.IsDir() {
			if err := cm.walkRollouts(ctx, path, id, matches); err != nil {
				return err
			}
			continue
		}
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if got := idFromRolloutPath(path); got != id {
			continue
		}
		*matches = append(*matches, path)
	}
	return nil
}

func (cm *codexManager) checkRecent(m model.SessionMeta, files []itemFile) error {
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

// deleteSession invokes `codex delete --force <id>`. The ID is a validated
// UUID and CODEX_HOME pins the CLI to the configured root.
func (cm *codexManager) deleteSession(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return errors.New("invalid codex session ID format")
	}
	return cm.exec(ctx, []string{"codex", "delete", "--force", id}, cm.root, []string{"CODEX_HOME=" + cm.root})
}

func idFromRolloutPath(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".jsonl")
	name = strings.TrimPrefix(name, "rollout-")
	if len(name) >= 36 {
		tail := name[len(name)-36:]
		if uuidPattern.MatchString(tail) {
			return tail
		}
	}
	if len(name) > 20 && (name[10] == 'T' || name[10] == 't') && name[19] == '-' {
		return name[20:]
	}
	return name
}

// runStandalone runs argv with no shell and bounded stderr.
func runStandalone(ctx context.Context, argv []string, dir string, env []string) error {
	if len(argv) == 0 {
		return errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &limitWriter{w: &buf, limit: 4096}
	if err := cmd.Run(); err != nil {
		return errors.New("execution failed")
	}
	return nil
}

type limitWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n >= l.limit {
		return len(p), nil
	}
	remaining := l.limit - l.n
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return len(p), err
}
