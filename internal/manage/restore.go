package manage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

var (
	// ErrRestoreNotAllowed is returned when restore is disabled in settings.
	ErrRestoreNotAllowed = errors.New("manage: restore is not allowed; enable it in settings first")
	// ErrRestoreCollision is returned when a session already exists and collision policy blocks it.
	ErrRestoreCollision = errors.New("manage: session already exists (choose copy mode to restore with a new ID)")
	// ErrRestoreLive is returned when the target session is currently active.
	ErrRestoreLive = errors.New("manage: target session is currently active")
	// ErrRestoreNoNative is returned when the bundle lacks native records for the target agent.
	ErrRestoreNoNative = errors.New("manage: bundle does not contain native records for this agent")
)

// CollisionPolicy governs what to do when a restored session ID collides with an existing session.
type CollisionPolicy string

const (
	// CollisionBlock blocks the restore if any destination session already exists.
	CollisionBlock CollisionPolicy = "block"
	// CollisionCopy restores the session under a new random UUID so both can coexist.
	CollisionCopy CollisionPolicy = "copy"
)

// RestoreRequest configures a bundle restoration.
type RestoreRequest struct {
	Bundle      *bundle.Bundle  `json:"-"`
	TargetAgent model.AgentID   `json:"targetAgent"`
	TargetCWD   string          `json:"targetCwd,omitempty"`
	Collision   CollisionPolicy `json:"collision,omitempty"`
	// CopyID pins the session id chosen for a copy restore. Preview mints it;
	// the confirm call must pass it back so re-planning reproduces the plan
	// the token was sealed over. A caller-supplied id is only honored for
	// copy restores and only when it is a UUID.
	CopyID string `json:"copyId,omitempty"`
}

// RestorePreviewItem details one session to be restored.
type RestorePreviewItem struct {
	SourceRef model.SessionRef `json:"sourceRef"`
	TargetRef model.SessionRef `json:"targetRef"`
	TargetCWD string           `json:"targetCwd"`
	Paths     []string         `json:"paths"`
	Bytes     int64            `json:"bytes"`
	Blocked   string           `json:"blocked,omitempty"`
	Warning   string           `json:"warning,omitempty"`
	IsCopy    bool             `json:"isCopy"`
}

// RestorePreview presents the planned restore operations and an HMAC token.
type RestorePreview struct {
	Items      []RestorePreviewItem `json:"items"`
	TotalBytes int64                `json:"totalBytes"`
	Token      string               `json:"token"`
	// CopyID is the session id minted for a copy restore. The confirm call
	// must set it on the request so re-planning reproduces this plan.
	CopyID string `json:"copyId,omitempty"`
}

// RestoreResultItem details the outcome of one restored session.
type RestoreResultItem struct {
	Ref     model.SessionRef `json:"ref"`
	Title   string           `json:"title"`
	OK      bool             `json:"ok"`
	Error   string           `json:"error,omitempty"`
	Created []string         `json:"created,omitempty"`
}

// RestoreReport summarizes the results of an executed restore.
type RestoreReport struct {
	Items       []RestoreResultItem `json:"items"`
	Restored    int                 `json:"restored"`
	Failed      int                 `json:"failed"`
	TotalBytes  int64               `json:"totalBytes"`
	CreatedRefs []model.SessionRef  `json:"createdRefs"`
}

type restoreOperation struct {
	Item        RestorePreviewItem
	ClaudeOp    *claudeRestoreOp
	CodexOp     *codexRestoreOp
	OpenCodeOp  *opencodeRestoreOp
	TargetAgent model.AgentID
}

type sealedRestoreOp struct {
	SourceRef model.SessionRef `json:"sourceRef"`
	TargetRef model.SessionRef `json:"targetRef"`
	TargetCWD string           `json:"targetCwd"`
	Paths     []string         `json:"paths"`
	IsCopy    bool             `json:"isCopy"`
	Blocked   string           `json:"blocked,omitempty"`
}

// PreviewRestore plans a session restore from a validated bundle, checks safety and collisions,
// and returns an HMAC token valid for tokenTTL (5 minutes).
func (m *Manager) PreviewRestore(
	ctx context.Context,
	req RestoreRequest,
	catalog []model.SessionMeta,
) (*RestorePreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.AllowRestore {
		return nil, ErrRestoreNotAllowed
	}

	ops, items, err := m.planRestore(ctx, req, catalog, cfg)
	if err != nil {
		return nil, err
	}
	// Hand the minted copy id back so the confirm call can re-plan identically.
	copyID := ""
	for i := range ops {
		if ops[i].Item.IsCopy {
			copyID = ops[i].Item.TargetRef.ID
			break
		}
	}

	var totalBytes int64
	for _, it := range items {
		totalBytes += it.Bytes
	}

	token := m.sealRestore(m.now().Add(tokenTTL), ops)
	return &RestorePreview{
		Items:      items,
		TotalBytes: totalBytes,
		Token:      token,
		CopyID:     copyID,
	}, nil
}

// Restore validates the HMAC token, re-checks safety against current disk and catalog state,
// and restores the session records atomically to the target provider's store.
func (m *Manager) Restore(
	ctx context.Context,
	req RestoreRequest,
	catalog []model.SessionMeta,
	token string,
) (*RestoreReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.AllowRestore {
		return nil, ErrRestoreNotAllowed
	}

	expiry, mac, err := decodeToken(token)
	if err != nil || !m.now().Before(expiry) {
		return nil, ErrPreviewStale
	}

	ops, _, err := m.planRestore(ctx, req, catalog, cfg)
	if err != nil {
		return nil, err
	}

	if !hmac.Equal(mac, m.macRestore(expiry, ops)) {
		return nil, ErrPreviewStale
	}

	report := &RestoreReport{
		Items: make([]RestoreResultItem, 0, len(ops)),
	}

	for _, op := range ops {
		if op.Item.Blocked != "" {
			return nil, fmt.Errorf("manage: item is blocked: %s", op.Item.Blocked)
		}

		var res RestoreResultItem
		var execErr error

		switch op.TargetAgent {
		case model.AgentClaude:
			if m.claude == nil || op.ClaudeOp == nil {
				return nil, ErrUnsupportedAction
			}
			res, execErr = m.claude.executeRestore(ctx, *op.ClaudeOp, req.Bundle)
		case model.AgentCodex:
			if m.codex == nil || op.CodexOp == nil {
				return nil, ErrUnsupportedAction
			}
			res, execErr = m.codex.executeRestore(ctx, *op.CodexOp, req.Bundle)
		case model.AgentOpenCode:
			if m.opencode == nil || op.OpenCodeOp == nil {
				return nil, ErrUnsupportedAction
			}
			res, execErr = m.opencode.executeRestore(ctx, *op.OpenCodeOp, req.Bundle)
		default:
			return nil, fmt.Errorf("manage: restore not yet supported for agent %s", op.TargetAgent)
		}

		report.Items = append(report.Items, res)
		if execErr == nil && res.OK {
			report.Restored++
			report.TotalBytes += op.Item.Bytes
			report.CreatedRefs = append(report.CreatedRefs, res.Ref)
			if op.ClaudeOp != nil && len(op.ClaudeOp.ChildRemaps) > 0 {
				for _, newChildID := range op.ClaudeOp.ChildRemaps {
					report.CreatedRefs = append(report.CreatedRefs, model.SessionRef{
						Agent: op.TargetAgent,
						ID:    newChildID,
					})
				}
			}
		} else {
			report.Failed++
			if execErr != nil {
				return report, execErr
			}
		}
	}

	return report, nil
}

func (m *Manager) planRestore(
	ctx context.Context,
	req RestoreRequest,
	catalog []model.SessionMeta,
	cfg Config,
) ([]restoreOperation, []RestorePreviewItem, error) {
	if req.Bundle == nil {
		return nil, nil, errors.New("manage: missing bundle")
	}

	targetAgent := req.TargetAgent
	if targetAgent == "" {
		targetAgent = req.Bundle.Manifest.Source.Agent
	}

	switch targetAgent {
	case model.AgentClaude:
		if m.claude == nil {
			return nil, nil, ErrUnsupportedAction
		}
		cop, err := m.claude.planRestore(ctx, req, catalog, m.live)
		if err != nil {
			return nil, nil, err
		}
		op := restoreOperation{
			Item:        cop.Item,
			ClaudeOp:    &cop,
			TargetAgent: targetAgent,
		}
		return []restoreOperation{op}, []RestorePreviewItem{cop.Item}, nil

	case model.AgentCodex:
		if m.codex == nil {
			return nil, nil, ErrUnsupportedAction
		}
		cxop, err := m.codex.planRestore(ctx, req, catalog, m.live)
		if err != nil {
			return nil, nil, err
		}
		op := restoreOperation{
			Item:        cxop.Item,
			CodexOp:     &cxop,
			TargetAgent: targetAgent,
		}
		return []restoreOperation{op}, []RestorePreviewItem{cxop.Item}, nil

	case model.AgentOpenCode:
		if m.opencode == nil {
			return nil, nil, ErrUnsupportedAction
		}
		oop, err := m.opencode.planRestore(ctx, req, catalog, m.live)
		if err != nil {
			return nil, nil, err
		}
		op := restoreOperation{
			Item:        oop.Item,
			OpenCodeOp:  &oop,
			TargetAgent: targetAgent,
		}
		return []restoreOperation{op}, []RestorePreviewItem{oop.Item}, nil

	default:
		return nil, nil, fmt.Errorf("manage: restore not supported for %s", targetAgent)
	}
}

func (m *Manager) macRestore(expiry time.Time, ops []restoreOperation) []byte {
	data := make([]sealedRestoreOp, 0, len(ops))
	for _, op := range ops {
		data = append(data, sealedRestoreOp{
			SourceRef: op.Item.SourceRef,
			TargetRef: op.Item.TargetRef,
			TargetCWD: op.Item.TargetCWD,
			Paths:     op.Item.Paths,
			IsCopy:    op.Item.IsCopy,
			Blocked:   op.Item.Blocked,
		})
	}
	encoded, _ := json.Marshal(data)
	h := hmac.New(sha256.New, m.tokenKey)
	_, _ = fmt.Fprintf(h, "restore:%d:", expiry.UnixMilli())
	_, _ = h.Write(encoded)
	return h.Sum(nil)
}

func (m *Manager) sealRestore(expiry time.Time, ops []restoreOperation) string {
	return fmt.Sprintf("%d:%s", expiry.UnixMilli(), hex.EncodeToString(m.macRestore(expiry, ops)))
}
