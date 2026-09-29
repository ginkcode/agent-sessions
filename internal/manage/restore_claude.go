package manage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// EncodeClaudeProjectDir mirrors Claude Code's project directory encoding.
// Every character not in [a-zA-Z0-9] is replaced 1:1 with '-'.
func EncodeClaudeProjectDir(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func newRandomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type claudeRestoreFile struct {
	ZipName  string
	DestPath string
	Size     int64
	IsJSONL  bool
}

type claudeRestoreOp struct {
	Item         RestorePreviewItem
	Files        []claudeRestoreFile
	OrigID       string
	NewID        string
	OrigCWD      string
	TargetCWD    string
	IsCopy       bool
	ChildRemaps  map[string]string // old child ref ID -> new child ref ID
}

func (cm *claudeManager) planRestore(
	ctx context.Context,
	req RestoreRequest,
	catalog []model.SessionMeta,
	live LiveFunc,
) (claudeRestoreOp, error) {
	b := req.Bundle
	if b == nil {
		return claudeRestoreOp{}, errors.New("manage: missing bundle")
	}

	origID := b.Manifest.Source.ID
	origCWD := b.Manifest.Source.CWD
	targetCWD := req.TargetCWD
	if targetCWD == "" {
		targetCWD = origCWD
	}
	if !filepath.IsAbs(targetCWD) {
		return claudeRestoreOp{}, errors.New("manage: target CWD must be an absolute path")
	}

	encNewCWD := EncodeClaudeProjectDir(targetCWD)
	projectDir := filepath.Join(cm.root, "projects", encNewCWD)

	// The project directory is created during restore, so it may not exist yet.
	if err := cm.ps.ensureWithinRoot(projectDir); err != nil {
		return claudeRestoreOp{}, err
	}

	// Determine new session ID and check collisions
	newID := origID
	isCopy := req.Collision == CollisionCopy
	var blockedReason string

	// Check collision in catalog
	catalogCollision := false
	for _, s := range catalog {
		if s.Ref.Agent == model.AgentClaude && (s.Ref.ID == origID || s.ParentID == origID) {
			catalogCollision = true
			break
		}
	}

	// Check collision on disk
	diskCollision := false
	mainJSONL := filepath.Join(projectDir, origID+".jsonl")
	sessionDir := filepath.Join(projectDir, origID)
	if _, err := os.Lstat(mainJSONL); err == nil {
		diskCollision = true
	} else if fi, err := os.Lstat(sessionDir); err == nil && fi.IsDir() {
		diskCollision = true
	} else {
		// Check any other project directory for same session ID
		matches, _ := filepath.Glob(filepath.Join(cm.root, "projects", "*", origID+".jsonl"))
		if len(matches) > 0 {
			diskCollision = true
		}
	}

	if catalogCollision || diskCollision {
		if isCopy {
			// Reuse the id pinned by the preview so the confirm token matches.
			if uuidPattern.MatchString(req.CopyID) {
				newID = req.CopyID
			} else {
				newID = newRandomUUID()
			}
		} else {
			blockedReason = fmt.Sprintf("session %s already exists; select 'Restore as copy' to create a new session", origID)
		}
	}

	// Liveness check (if not already blocked)
	if blockedReason == "" && live != nil {
		isLive, err := live(ctx, string(model.AgentClaude), newID)
		if err != nil {
			blockedReason = "liveness check failed"
		} else if isLive {
			blockedReason = "session is currently active/live"
		}
	}

	childRemaps := make(map[string]string)
	var restoreFiles []claudeRestoreFile
	var totalBytes int64
	var destPaths []string

	// Find all native entries in the bundle
	for _, s := range b.Manifest.Sessions {
		if s.Ref.Agent != model.AgentClaude {
			continue
		}
		for _, nf := range s.Native {
			destRel, err := claudeDestRel(nf.RootRel, origID, newID, encNewCWD)
			if err != nil {
				return claudeRestoreOp{}, fmt.Errorf("manage: derive claude path: %w", err)
			}
			destPath := filepath.Join(cm.root, destRel)

			// Destination files are created by the restore, so they may not exist yet.
			if err := cm.ps.ensureWithinRoot(destPath); err != nil {
				return claudeRestoreOp{}, fmt.Errorf("manage: path safety violation: %w", err)
			}

			// Pre-flight check: destination must not already exist on disk
			if _, err := os.Lstat(destPath); err == nil && blockedReason == "" {
				blockedReason = fmt.Sprintf("file %s already exists on disk", filepath.Base(destPath))
			}

			isJSONL := strings.HasSuffix(destPath, ".jsonl")
			restoreFiles = append(restoreFiles, claudeRestoreFile{
				ZipName:  nf.Name,
				DestPath: destPath,
				Size:     nf.Size,
				IsJSONL:  isJSONL,
			})
			totalBytes += nf.Size
			destPaths = append(destPaths, destPath)
		}

		if s.ParentID != "" && isCopy {
			// e.g. old ID "origID/agent-abc" -> "newID/agent-abc"
			tail := strings.TrimPrefix(s.Ref.ID, origID+"/")
			childRemaps[s.Ref.ID] = newID + "/" + tail
		}
	}

	if len(restoreFiles) == 0 {
		return claudeRestoreOp{}, ErrRestoreNoNative
	}

	item := RestorePreviewItem{
		SourceRef: model.SessionRef{Agent: model.AgentClaude, ID: origID},
		TargetRef: model.SessionRef{Agent: model.AgentClaude, ID: newID},
		TargetCWD: targetCWD,
		Paths:     destPaths,
		Bytes:     totalBytes,
		Blocked:   blockedReason,
		IsCopy:    isCopy,
	}

	return claudeRestoreOp{
		Item:        item,
		Files:       restoreFiles,
		OrigID:      origID,
		NewID:       newID,
		OrigCWD:     origCWD,
		TargetCWD:   targetCWD,
		IsCopy:      isCopy,
		ChildRemaps: childRemaps,
	}, nil
}

func claudeDestRel(rootRel string, origID string, newID string, encNewCWD string) (string, error) {
	norm := filepath.ToSlash(filepath.Clean(rootRel))
	parts := strings.Split(norm, "/")
	if len(parts) < 3 || parts[0] != "projects" {
		return "", fmt.Errorf("invalid claude root rel path %q: must start with projects/", rootRel)
	}

	tail := strings.Join(parts[2:], "/")
	if tail == origID+".jsonl" {
		return filepath.Join("projects", encNewCWD, newID+".jsonl"), nil
	}
	if strings.HasPrefix(tail, origID+"/") {
		subTail := strings.TrimPrefix(tail, origID+"/")
		return filepath.Join("projects", encNewCWD, newID, filepath.FromSlash(subTail)), nil
	}

	return "", fmt.Errorf("path %q does not match session ID %q", rootRel, origID)
}

func (cm *claudeManager) executeRestore(
	ctx context.Context,
	op claudeRestoreOp,
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

		var payload []byte
		var err error
		if rf.IsJSONL && (op.TargetCWD != op.OrigCWD || op.IsCopy) {
			newSessionID := ""
			if op.IsCopy {
				newSessionID = op.NewID
			}
			payload, err = rewriteClaudeJSONL(raw, op.TargetCWD, newSessionID)
			if err != nil {
				rollback()
				result.Error = err.Error()
				return result, err
			}
		} else {
			payload = raw
		}

		dir := filepath.Dir(rf.DestPath)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			rollback()
			result.Error = fmt.Errorf("mkdir %s: %w", dir, err).Error()
			return result, err
		}

		if _, err := os.Lstat(rf.DestPath); err == nil {
			rollback()
			err = fmt.Errorf("destination file %s already exists, refusing to overwrite", rf.DestPath)
			result.Error = err.Error()
			return result, err
		}

		tmp, err := os.CreateTemp(dir, ".restore-*.tmp")
		if err != nil {
			rollback()
			result.Error = fmt.Errorf("create temp file: %w", err).Error()
			return result, err
		}
		tmpName := tmp.Name()

		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			rollback()
			result.Error = fmt.Errorf("chmod temp file: %w", err).Error()
			return result, err
		}

		if _, err := tmp.Write(payload); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			rollback()
			result.Error = fmt.Errorf("write temp file: %w", err).Error()
			return result, err
		}

		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
			rollback()
			result.Error = fmt.Errorf("sync temp file: %w", err).Error()
			return result, err
		}

		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpName)
			rollback()
			result.Error = fmt.Errorf("close temp file: %w", err).Error()
			return result, err
		}

		if err := os.Rename(tmpName, rf.DestPath); err != nil {
			_ = os.Remove(tmpName)
			rollback()
			result.Error = fmt.Errorf("rename to %s: %w", rf.DestPath, err).Error()
			return result, err
		}

		created = append(created, rf.DestPath)
	}

	result.OK = true
	result.Created = created
	return result, nil
}

func rewriteClaudeJSONL(data []byte, newCWD string, newSessionID string) ([]byte, error) {
	if newCWD == "" && newSessionID == "" {
		return data, nil
	}
	cwdJSON, _ := json.Marshal(newCWD)
	var sessionIDJSON []byte
	if newSessionID != "" {
		sessionIDJSON, _ = json.Marshal(newSessionID)
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
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(line, &fields); err != nil {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		changed := false
		if newCWD != "" {
			if _, ok := fields["cwd"]; ok {
				fields["cwd"] = cwdJSON
				changed = true
			}
		}
		if newSessionID != "" {
			if _, ok := fields["sessionId"]; ok {
				fields["sessionId"] = sessionIDJSON
				changed = true
			}
		}
		if !changed {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		marshaled, err := json.Marshal(fields)
		if err != nil {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		out.Write(marshaled)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan claude jsonl: %w", err)
	}
	return out.Bytes(), nil
}
