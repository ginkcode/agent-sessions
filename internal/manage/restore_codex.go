package manage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// codexRestoreFile is one rollout to place under sessions/YYYY/MM/DD.
type codexRestoreFile struct {
	ZipName  string
	DestPath string
	Size     int64
	Session  string
}

type codexRestoreOp struct {
	Item      RestorePreviewItem
	Files     []codexRestoreFile
	OrigID    string
	NewID     string
	OrigCWD   string
	TargetCWD string
	IsCopy    bool
}

func (cm *codexManager) planRestore(
	ctx context.Context,
	req RestoreRequest,
	catalog []model.SessionMeta,
	live LiveFunc,
) (codexRestoreOp, error) {
	b := req.Bundle
	if b == nil {
		return codexRestoreOp{}, errors.New("manage: missing bundle")
	}

	origID := b.Manifest.Source.ID
	if !uuidPattern.MatchString(origID) {
		return codexRestoreOp{}, errors.New("manage: invalid codex session id")
	}
	origCWD := b.Manifest.Source.CWD
	targetCWD := req.TargetCWD
	if targetCWD == "" {
		targetCWD = origCWD
	}
	if !filepath.IsAbs(targetCWD) {
		return codexRestoreOp{}, errors.New("manage: target CWD must be an absolute path")
	}

	day := cm.now().UTC().Format("2006/01/02")
	sessionsDir := filepath.Join(cm.root, "sessions", day)
	if err := cm.ps.ensureWithinRoot(filepath.Join(sessionsDir, "rollout.jsonl")); err != nil {
		return codexRestoreOp{}, err
	}

	newID := origID
	isCopy := req.Collision == CollisionCopy
	var blockedReason string

	catalogCollision := false
	for _, s := range catalog {
		if s.Ref.Agent == model.AgentCodex && (s.Ref.ID == origID || s.ParentID == origID) {
			catalogCollision = true
			break
		}
	}
	diskCollision := false
	for _, sub := range []string{"sessions", "archived_sessions"} {
		base := filepath.Join(cm.root, sub)
		if _, err := os.Lstat(base); err != nil {
			continue
		}
		var matches []string
		if err := cm.walkRollouts(ctx, base, origID, &matches); err != nil {
			return codexRestoreOp{}, err
		}
		if len(matches) > 0 {
			diskCollision = true
			break
		}
	}

	if catalogCollision || diskCollision {
		if isCopy {
			if uuidPattern.MatchString(req.CopyID) {
				newID = req.CopyID
			} else {
				newID = newRandomUUID()
			}
		} else {
			blockedReason = fmt.Sprintf("session %s already exists; select 'Restore as copy' to create a new session", origID)
		}
	}

	if blockedReason == "" && live != nil {
		isLive, err := live(ctx, string(model.AgentCodex), newID)
		if err != nil {
			blockedReason = "liveness check failed"
		} else if isLive {
			blockedReason = "session is currently active/live"
		}
	}

	var files []codexRestoreFile
	var totalBytes int64
	var destPaths []string
	for _, s := range b.Manifest.Sessions {
		if s.Ref.Agent != model.AgentCodex {
			continue
		}
		sessionID := s.Ref.ID
		if sessionID == origID {
			sessionID = newID
		} else if isCopy && s.ParentID == origID {
			// Descendants keep their own ids; a collision on one blocks below.
			sessionID = s.Ref.ID
		}
		if !uuidPattern.MatchString(sessionID) {
			return codexRestoreOp{}, errors.New("manage: invalid codex session id")
		}
		for _, nf := range s.Native {
			if !strings.HasPrefix(filepath.Base(nf.RootRel), "rollout-") || !strings.HasSuffix(nf.RootRel, ".jsonl") {
				return codexRestoreOp{}, fmt.Errorf("manage: %q is not a codex rollout", nf.RootRel)
			}
			dest := filepath.Join(sessionsDir, "rollout-restored-"+sessionID+".jsonl")
			if err := cm.ps.ensureWithinRoot(dest); err != nil {
				return codexRestoreOp{}, fmt.Errorf("manage: path safety violation: %w", err)
			}
			if _, err := os.Lstat(dest); err == nil && blockedReason == "" {
				blockedReason = fmt.Sprintf("file %s already exists on disk", filepath.Base(dest))
			}
			files = append(files, codexRestoreFile{
				ZipName:  nf.Name,
				DestPath: dest,
				Size:     nf.Size,
				Session:  sessionID,
			})
			totalBytes += nf.Size
			destPaths = append(destPaths, dest)
		}
	}
	if len(files) == 0 {
		return codexRestoreOp{}, ErrRestoreNoNative
	}

	return codexRestoreOp{
		Item: RestorePreviewItem{
			SourceRef: model.SessionRef{Agent: model.AgentCodex, ID: origID},
			TargetRef: model.SessionRef{Agent: model.AgentCodex, ID: newID},
			TargetCWD: targetCWD,
			Paths:     destPaths,
			Bytes:     totalBytes,
			Blocked:   blockedReason,
			IsCopy:    isCopy,
		},
		Files:     files,
		OrigID:    origID,
		NewID:     newID,
		OrigCWD:   origCWD,
		TargetCWD: targetCWD,
		IsCopy:    isCopy,
	}, nil
}

func (cm *codexManager) executeRestore(
	ctx context.Context,
	op codexRestoreOp,
	b *bundle.Bundle,
) (RestoreResultItem, error) {
	result := RestoreResultItem{
		Ref:   op.Item.TargetRef,
		Title: b.Manifest.Source.Title,
	}
	var created []string
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.Remove(created[i])
		}
	}

	for _, rf := range op.Files {
		if err := ctx.Err(); err != nil {
			rollback()
			result.Error = err.Error()
			return result, err
		}
		raw, ok := b.Native[rf.ZipName]
		if !ok {
			rollback()
			err := fmt.Errorf("native file %q missing from bundle", rf.ZipName)
			result.Error = err.Error()
			return result, err
		}
		sessionID := ""
		if op.IsCopy && rf.Session == op.NewID {
			sessionID = op.NewID
		}
		payload, err := rewriteCodexRollout(raw, op.TargetCWD, sessionID)
		if err != nil {
			rollback()
			result.Error = err.Error()
			return result, err
		}
		if err := writeRestoredFile(rf.DestPath, payload); err != nil {
			rollback()
			result.Error = err.Error()
			return result, err
		}
		created = append(created, rf.DestPath)
	}

	result.OK = true
	result.Created = created
	return result, nil
}

// rewriteCodexRollout rewrites the cwd inside session_meta and turn_context
// payloads, and the session id inside session_meta when restoring as a copy.
// Every other record is copied verbatim: response items and events carry no
// working directory of their own.
func rewriteCodexRollout(data []byte, newCWD, newSessionID string) ([]byte, error) {
	if newCWD == "" && newSessionID == "" {
		return data, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 64*1024*1024)

	var out bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		rewritten, err := rewriteCodexLine(line, newCWD, newSessionID)
		if err != nil {
			return nil, err
		}
		out.Write(rewritten)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan codex rollout: %w", err)
	}
	return out.Bytes(), nil
}

func rewriteCodexLine(line []byte, newCWD, newSessionID string) ([]byte, error) {
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(line, &rec); err != nil {
		return line, nil
	}
	typ := rawString(rec["type"])
	if typ != "session_meta" && typ != "turn_context" {
		return line, nil
	}
	payloadRaw, ok := rec["payload"]
	if !ok {
		return line, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return line, nil
	}
	changed := false
	if newCWD != "" {
		if _, ok := payload["cwd"]; ok {
			payload["cwd"], _ = json.Marshal(newCWD)
			changed = true
		}
	}
	if newSessionID != "" && typ == "session_meta" {
		if _, ok := payload["id"]; ok {
			payload["id"], _ = json.Marshal(newSessionID)
			changed = true
		}
	}
	if !changed {
		return line, nil
	}
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return line, nil
	}
	rec["payload"] = rewritten
	return json.Marshal(rec)
}

func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// writeRestoredFile writes payload to dest at 0600 through a temp file and
// rename, refusing when dest already exists.
func writeRestoredFile(dest string, payload []byte) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("destination file %s already exists, refusing to overwrite", dest)
	}
	tmp, err := os.CreateTemp(dir, ".restore-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(fmt.Errorf("chmod temp file: %w", err))
	}
	if _, err := tmp.Write(payload); err != nil {
		return fail(fmt.Errorf("write temp file: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("sync temp file: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename to %s: %w", dest, err)
	}
	return nil
}
