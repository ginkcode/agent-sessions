package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// CapturedFile is one verbatim native record safe to copy into a bundle.
// Rel is relative to the provider storage root and uses slash separators.
type CapturedFile struct {
	Path string
	Rel  string
	Size int64
}

// Capture is one session (the root or a descendant) and the native files that
// belong to it. OpenCode sessions carry no files: their export lives in Body.
type Capture struct {
	Ref      model.SessionRef
	ParentID string
	Files    []CapturedFile
	Body     []byte
}

// OpenCodeExportFunc runs `opencode session export` and returns its stdout.
// argv[0] is the executable name. Tests substitute a fake; production uses
// execOpenCodeExport.
type OpenCodeExportFunc func(ctx context.Context, argv []string, dir string, env []string) ([]byte, error)

// CaptureSession returns the native records of root and its descendants.
// It is read-only: unlike deletion planning it ignores liveness and recency,
// because exporting a live session does not touch the tool's store. Every
// path is re-derived and classified, so a protected name or a symlink is
// refused rather than copied.
func (m *Manager) CaptureSession(ctx context.Context, root model.SessionMeta, catalog []model.SessionMeta) ([]Capture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch root.Ref.Agent {
	case model.AgentClaude:
		return m.captureClaude(ctx, root, catalog)
	case model.AgentCodex:
		return m.captureCodex(ctx, root, catalog)
	case model.AgentOpenCode:
		return m.captureOpenCode(ctx, root)
	default:
		return nil, ErrUnsupportedAction
	}
}

func (m *Manager) captureClaude(ctx context.Context, root model.SessionMeta, catalog []model.SessionMeta) ([]Capture, error) {
	if m.claude == nil {
		return nil, ErrUnsupportedAction
	}
	idx := newCatalogIndex(catalog)
	children := []model.SessionMeta(nil)
	if root.ParentID == "" {
		var err error
		children, err = m.claudeChildren(ctx, root, idx)
		if err != nil {
			return nil, err
		}
	}
	out := make([]Capture, 0, 1+len(children))
	for _, meta := range append([]model.SessionMeta{root}, children...) {
		paths, err := m.claude.derivePaths(ctx, meta)
		if err != nil {
			return nil, safePathError(err)
		}
		files, err := captureFiles(m.claude.ps, m.claude.root, paths)
		if err != nil {
			return nil, err
		}
		if meta.ParentID == "" && len(children) > 0 {
			files = excludeDescendantFiles(files, children)
		}
		out = append(out, Capture{Ref: meta.Ref, ParentID: meta.ParentID, Files: files})
	}
	return out, nil
}

func (m *Manager) captureCodex(ctx context.Context, root model.SessionMeta, catalog []model.SessionMeta) ([]Capture, error) {
	if m.codex == nil {
		return nil, ErrUnsupportedAction
	}
	idx := newCatalogIndex(catalog)
	children, err := idx.descendants(root.Ref)
	if err != nil {
		return nil, err
	}
	// Parent before children reads more naturally in a bundle than the
	// child-first order deletion uses.
	sessions := append([]model.SessionMeta{root}, children...)
	out := make([]Capture, 0, len(sessions))
	for _, meta := range sessions {
		paths, err := m.codex.derivePaths(ctx, meta)
		if err != nil {
			return nil, safePathError(err)
		}
		if len(paths) != 1 {
			return nil, errors.New("codex session has ambiguous rollout paths")
		}
		files, err := captureFiles(m.codex.ps, m.codex.root, paths)
		if err != nil {
			return nil, err
		}
		out = append(out, Capture{Ref: meta.Ref, ParentID: meta.ParentID, Files: files})
	}
	return out, nil
}

func (m *Manager) captureOpenCode(ctx context.Context, root model.SessionMeta) ([]Capture, error) {
	if m.opencode == nil {
		return nil, ErrUnsupportedAction
	}
	members, err := m.opencode.members(ctx, root)
	if err != nil {
		return nil, err
	}
	export := m.opencodeExport
	if export == nil {
		export = execOpenCodeExport
	}
	out := make([]Capture, 0, len(members))
	for _, member := range members {
		body, err := m.opencode.exportSession(ctx, member.ID, export)
		if err != nil {
			return nil, err
		}
		parent := ""
		if member.ID != root.Ref.ID {
			parent = root.Ref.ID
		}
		out = append(out, Capture{
			Ref:      model.SessionRef{Agent: model.AgentOpenCode, ID: member.ID},
			ParentID: parent,
			Body:     body,
		})
	}
	return out, nil
}

// captureFiles classifies every path (protected names and symlinks included)
// and reports each regular file. A directory is expanded to the regular
// files inside it; the directory entry itself is not a file to copy.
func captureFiles(ps *pathSafety, root string, paths []string) ([]CapturedFile, error) {
	root = filepath.Clean(root)
	var out []CapturedFile
	for _, p := range paths {
		mode, err := ps.classify(p)
		if err != nil {
			return nil, safePathError(err)
		}
		if mode.IsDir() {
			if err := ps.checkTree(func() error { return nil }, p); err != nil {
				return nil, safePathError(err)
			}
			nested, err := regularFiles(p)
			if err != nil {
				return nil, err
			}
			for _, nestedPath := range nested {
				file, err := capturedFile(root, nestedPath)
				if err != nil {
					return nil, err
				}
				out = append(out, file)
			}
			continue
		}
		file, err := capturedFile(root, p)
		if err != nil {
			return nil, err
		}
		out = append(out, file)
	}
	return out, nil
}

// excludeDescendantFiles drops files that belong to a descendant session,
// so the root capture and the child captures do not both carry them.
func excludeDescendantFiles(files []CapturedFile, children []model.SessionMeta) []CapturedFile {
	var drop []string
	for _, child := range children {
		if child.SourcePath == "" {
			continue
		}
		drop = append(drop, filepath.Clean(child.SourcePath), filepath.Dir(filepath.Clean(child.SourcePath)))
	}
	out := files[:0:0]
	for _, f := range files {
		skip := false
		for _, d := range drop {
			if f.Path == d || strings.HasPrefix(f.Path, d+string(filepath.Separator)) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
}

func regularFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("manage: cannot read session file")
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func capturedFile(root, path string) (CapturedFile, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return CapturedFile{}, ErrPathOutsideRoot
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return CapturedFile{}, errors.New("manage: cannot read session file")
	}
	return CapturedFile{Path: path, Rel: filepath.ToSlash(rel), Size: fi.Size()}, nil
}

// exportSession runs `opencode session export --standalone <id>` against the
// configured data root. The ID is validated again here because this is the
// last point before it reaches an argv.
func (om *opencodeManager) exportSession(ctx context.Context, id string, export OpenCodeExportFunc) ([]byte, error) {
	if strings.ContainsAny(id, "\x00\r\n\t ;&|`$") || strings.HasPrefix(id, "-") || !isSafeID(id) {
		return nil, errors.New("invalid opencode session id")
	}
	if !om.canTargetRoot() {
		return nil, errors.New("opencode root is non-standard; refusing to execute CLI")
	}
	xdgDataHome := filepath.Dir(filepath.Clean(om.root))
	env := []string{
		"XDG_DATA_HOME=" + xdgDataHome,
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_MODELS_FETCH=1",
	}
	body, err := export(ctx, []string{"opencode", "session", "export", "--standalone", id}, om.root, env)
	if err != nil {
		return nil, fmt.Errorf("opencode export failed: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("opencode export produced no output")
	}
	return body, nil
}

// execOpenCodeExport is the production OpenCodeExportFunc. It resolves the
// CLI the same way deletion does and keeps stdout, which is the export.
func execOpenCodeExport(ctx context.Context, argv []string, dir string, env []string) ([]byte, error) {
	return runCapture(ctx, argv, dir, env)
}
