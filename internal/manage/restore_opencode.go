package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// opencodeRestoreFile is one standalone export to hand to `session import`.
type opencodeRestoreFile struct {
	ZipName string
	ID      string
	Size    int64
}

type opencodeRestoreOp struct {
	Item      RestorePreviewItem
	Files     []opencodeRestoreFile
	OrigID    string
	TargetCWD string
}

func (om *opencodeManager) planRestore(
	ctx context.Context,
	req RestoreRequest,
	catalog []model.SessionMeta,
	live LiveFunc,
) (opencodeRestoreOp, error) {
	b := req.Bundle
	if b == nil {
		return opencodeRestoreOp{}, errors.New("manage: missing bundle")
	}
	if !om.canTargetRoot() {
		return opencodeRestoreOp{}, errors.New("opencode root is non-standard; refusing to execute CLI")
	}

	origID := b.Manifest.Source.ID
	if !isSafeID(origID) {
		return opencodeRestoreOp{}, errors.New("manage: invalid opencode session id")
	}
	targetCWD := req.TargetCWD
	if targetCWD == "" {
		targetCWD = b.Manifest.Source.CWD
	}
	if !filepath.IsAbs(targetCWD) {
		return opencodeRestoreOp{}, errors.New("manage: target CWD must be an absolute path")
	}
	// Copy mode cannot mint a new id: `session import` preserves the exported
	// session id, so a collision can only be refused.
	if req.Collision == CollisionCopy {
		return opencodeRestoreOp{}, errors.New("manage: opencode import keeps the original session id; copy mode is not available")
	}

	var blockedReason string
	for _, s := range catalog {
		if s.Ref.Agent == model.AgentOpenCode && (s.Ref.ID == origID || s.ParentID == origID) {
			blockedReason = fmt.Sprintf("session %s already exists", origID)
			break
		}
	}
	if blockedReason == "" && live != nil {
		isLive, err := live(ctx, string(model.AgentOpenCode), origID)
		if err != nil {
			blockedReason = "liveness check failed"
		} else if isLive {
			blockedReason = "session is currently active/live"
		}
	}

	var files []opencodeRestoreFile
	var totalBytes int64
	for _, s := range b.Manifest.Sessions {
		if s.Ref.Agent != model.AgentOpenCode {
			continue
		}
		if !isSafeID(s.Ref.ID) {
			return opencodeRestoreOp{}, errors.New("manage: invalid opencode session id")
		}
		for _, nf := range s.Native {
			files = append(files, opencodeRestoreFile{ZipName: nf.Name, ID: s.Ref.ID, Size: nf.Size})
			totalBytes += nf.Size
		}
	}
	if len(files) == 0 {
		return opencodeRestoreOp{}, ErrRestoreNoNative
	}

	return opencodeRestoreOp{
		Item: RestorePreviewItem{
			SourceRef: model.SessionRef{Agent: model.AgentOpenCode, ID: origID},
			TargetRef: model.SessionRef{Agent: model.AgentOpenCode, ID: origID},
			TargetCWD: targetCWD,
			Bytes:     totalBytes,
			Blocked:   blockedReason,
		},
		Files:     files,
		OrigID:    origID,
		TargetCWD: targetCWD,
	}, nil
}

func (om *opencodeManager) executeRestore(
	ctx context.Context,
	op opencodeRestoreOp,
	b *bundle.Bundle,
) (RestoreResultItem, error) {
	result := RestoreResultItem{
		Ref:   op.Item.TargetRef,
		Title: b.Manifest.Source.Title,
	}
	// Children import first so a parent that references them finds them present.
	ordered := orderOpenCodeImports(op.Files, b.Manifest.Sessions)

	dir, err := os.MkdirTemp("", "agent-sessions-import-*")
	if err != nil {
		result.Error = err.Error()
		return result, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	var imported []string
	for _, rf := range ordered {
		if err := ctx.Err(); err != nil {
			result.Error = err.Error()
			return result, err
		}
		raw, ok := b.Native[rf.ZipName]
		if !ok {
			err := fmt.Errorf("native file %q missing from bundle", rf.ZipName)
			result.Error = err.Error()
			return result, err
		}
		file := filepath.Join(dir, rf.ID+".json")
		if err := os.WriteFile(file, raw, 0o600); err != nil {
			result.Error = err.Error()
			return result, err
		}
		if err := om.importSession(ctx, file, op.TargetCWD); err != nil {
			result.Error = err.Error()
			result.Created = imported
			return result, err
		}
		imported = append(imported, rf.ID)
	}
	result.OK = true
	result.Created = imported
	return result, nil
}

// orderOpenCodeImports puts child sessions ahead of their parent. The import
// keeps each session's id, and a parent export only references its children.
func orderOpenCodeImports(files []opencodeRestoreFile, sessions []bundle.SessionManifest) []opencodeRestoreFile {
	children := map[string]bool{}
	for _, s := range sessions {
		if s.ParentID != "" {
			children[s.Ref.ID] = true
		}
	}
	var first, rest []opencodeRestoreFile
	for _, f := range files {
		if children[f.ID] {
			first = append(first, f)
		} else {
			rest = append(rest, f)
		}
	}
	return append(first, rest...)
}

// importSession runs `opencode session import --standalone --directory <cwd>`.
// The file path is one this process just created, and the directory is the
// user-chosen target, so neither comes from the bundle's own paths.
func (om *opencodeManager) importSession(ctx context.Context, file, directory string) error {
	if !om.canTargetRoot() {
		return errors.New("opencode root is non-standard; refusing to execute CLI")
	}
	if strings.ContainsAny(file, "\x00\r\n") || strings.ContainsAny(directory, "\x00\r\n") {
		return errors.New("manage: import path contains a newline")
	}
	xdgDataHome := filepath.Dir(filepath.Clean(om.root))
	env := []string{
		"XDG_DATA_HOME=" + xdgDataHome,
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_MODELS_FETCH=1",
	}
	argv := []string{"opencode", "session", "import", "--standalone", "--directory", directory, file}
	return om.exec(ctx, argv, om.root, env)
}
