package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// discover lists rollout paths at exactly sessions/YYYY/MM/DD/rollout-*.jsonl
// and archived_sessions/rollout-*.jsonl. It never reads rollout content.
func (p *Provider) discover(ctx context.Context) ([]string, error) {
	return p.discoverWithDiagnostics(ctx, nil)
}

func (p *Provider) discoverWithDiagnostics(ctx context.Context, diag *provider.Diagnostics) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var files []string
	if err := p.visitDirs(ctx, filepath.Join(p.root, "sessions"), []int{4, 2, 2}, &files, diag); err != nil {
		return nil, err
	}
	if err := p.visitDirs(ctx, filepath.Join(p.root, "archived_sessions"), nil, &files, diag); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// visitDirs reads only the requested depth. widths [4,2,2] correspond to
// year/month/day components. An unreadable child is warned and skipped; the
// rest of the tree remains usable.
func (p *Provider) visitDirs(ctx context.Context, dir string, widths []int, files *[]string, diag *provider.Diagnostics) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			warn(diag, dir, "read directory: %v", err)
		}
		return nil
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if len(widths) > 0 {
			if entry.IsDir() && digits(name, widths[0]) {
				if err := p.visitDirs(ctx, path, widths[1:], files, diag); err != nil {
					return err
				}
			}
			continue
		}
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") || entry.IsDir() {
			continue
		}
		// Reject symlink escapes (and broken symlinks); only regular files are
		// sources. os.Open tests readability, unlike os.Stat.
		if !withinRoot(p.root, path) {
			warn(diag, path, "rollout symlink escapes Codex root or is broken")
			continue
		}
		info, err := entry.Info()
		if err != nil {
			warn(diag, path, "stat rollout: %v", err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			warn(diag, path, "open rollout: %v", err)
			continue
		}
		if err := f.Close(); err != nil {
			warn(diag, path, "close rollout: %v", err)
			continue
		}
		*files = append(*files, path)
	}
	return nil
}

func digits(s string, width int) bool {
	if len(s) != width {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// withinRoot resolves symlinks and checks containment. Unlike a lexical-only
// check it does not allow files pointing outside the configured Codex root.
func withinRoot(root, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	root = pathutil.NormalizeDir(root)
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func warn(diag *provider.Diagnostics, path, format string, args ...any) {
	if diag != nil {
		diag.Warn(path, 0, format, args...)
	}
}

// indexDBPath chooses the highest numeric state_N.sqlite in the root. It
// ignores malformed names, directories, and symlinks escaping the Codex root.
// No SQLite file is opened here.
func (p *Provider) indexDBPath() (string, error) {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("codex: read root %q: %w", p.root, err)
	}

	var bestN uint64
	var found bool
	var bestPath string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "state_") || !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		digitsPart := strings.TrimSuffix(strings.TrimPrefix(name, "state_"), ".sqlite")
		if len(digitsPart) == 0 || !digits(digitsPart, len(digitsPart)) {
			continue
		}
		n, err := strconv.ParseUint(digitsPart, 10, 64)
		if err != nil {
			continue
		}
		path := filepath.Join(p.root, name)
		if !withinRoot(p.root, path) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if !found || n > bestN {
			found, bestN, bestPath = true, n, path
		}
	}
	return bestPath, nil
}
