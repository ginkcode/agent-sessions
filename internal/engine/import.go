package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// BundleSummary details an inspected session bundle for review and import.
type BundleSummary struct {
	BundleID         string                 `json:"bundleId"`
	Path             string                 `json:"path"`
	Format           string                 `json:"format"`
	Version          int                    `json:"version"`
	CreatedAt        time.Time              `json:"createdAt"`
	AppVersion       string                 `json:"appVersion,omitempty"`
	Profile          string                 `json:"profile"` // "complete" or "share-safe"
	Source           bundle.SourceMeta      `json:"source"`
	SessionsCount    int                    `json:"sessionsCount"`
	NativeFilesCount int                    `json:"nativeFilesCount"`
	NativeBytes      int64                  `json:"nativeBytes"`
	RedactionRules   map[string]int         `json:"redactionRules,omitempty"`
	RedactionCounts  int                    `json:"redactionCounts"`
	HandoffTokens    int                    `json:"handoffTokens"`
	Verified         bool                   `json:"verified"`
	RestoreAvailable bool                   `json:"restoreAvailable"`
	HandoffAvailable bool                   `json:"handoffAvailable"`
	Sessions         []BundleSessionSummary `json:"sessions"`
}

// BundleSessionSummary details one session (root or subagent) inside a bundle.
type BundleSessionSummary struct {
	Ref         model.SessionRef `json:"ref"`
	ParentID    string           `json:"parentId,omitempty"`
	NativeFiles int              `json:"nativeFiles"`
	NativeBytes int64            `json:"nativeBytes"`
	Title       string           `json:"title,omitempty"`
}

// BundleHandoffRequest configures generating a cross-agent handoff from an imported bundle.
type BundleHandoffRequest struct {
	BundleID         string        `json:"bundleId"`
	Target           model.AgentID `json:"target"`
	Budget           int           `json:"budget"` // token budget; 0 = detailed (80k), -1 = unlimited
	IncludeReasoning bool          `json:"includeReasoning"`
	RedactSecrets    bool          `json:"redactSecrets"`
	CWD              string        `json:"cwd,omitempty"` // override or remapped target cwd
}

func summarizeBundle(bundleID, path string, b *bundle.Bundle) BundleSummary {
	var totalNativeFiles int
	var totalNativeBytes int64
	sessions := make([]BundleSessionSummary, 0, len(b.Manifest.Sessions))

	var transcripts []model.Transcript
	_ = json.Unmarshal(b.Transcript, &transcripts)
	titles := make(map[string]string, len(transcripts))
	for _, t := range transcripts {
		titles[t.Meta.Ref.Key()] = t.Meta.Title
	}

	for _, s := range b.Manifest.Sessions {
		var sBytes int64
		for _, n := range s.Native {
			sBytes += n.Size
		}
		totalNativeFiles += len(s.Native)
		totalNativeBytes += sBytes
		sessions = append(sessions, BundleSessionSummary{
			Ref:         s.Ref,
			ParentID:    s.ParentID,
			NativeFiles: len(s.Native),
			NativeBytes: sBytes,
			Title:       titles[s.Ref.Key()],
		})
	}

	handoffTokens := len(b.Handoff) / 4
	if handoffTokens == 0 && len(transcripts) > 0 {
		handoffTokens = len(b.Transcript) / 8
	}

	restoreAvailable := b.Manifest.Profile == bundle.ProfileComplete && totalNativeFiles > 0

	return BundleSummary{
		BundleID:         bundleID,
		Path:             path,
		Format:           b.Manifest.Format,
		Version:          b.Manifest.Version,
		CreatedAt:        b.Manifest.CreatedAt,
		AppVersion:       b.Manifest.AppVersion,
		Profile:          string(b.Manifest.Profile),
		Source:           b.Manifest.Source,
		SessionsCount:    len(b.Manifest.Sessions),
		NativeFilesCount: totalNativeFiles,
		NativeBytes:      totalNativeBytes,
		RedactionRules:   b.Manifest.Redaction.Rules,
		RedactionCounts:  b.Manifest.Redaction.Counts,
		HandoffTokens:    handoffTokens,
		Verified:         true,
		RestoreAvailable: restoreAvailable,
		HandoffAvailable: len(b.Handoff) > 0 || len(b.Transcript) > 0,
		Sessions:         sessions,
	}
}

func generateBundleID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("bndl-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// OpenBundle opens and validates a bundle from disk, caching it in memory.
func (s *Service) OpenBundle(ctx context.Context, path string) (BundleSummary, error) {
	b, err := bundle.ReadFile(path)
	if err != nil {
		return BundleSummary{}, fmt.Errorf("open bundle: %w", err)
	}
	id := generateBundleID()
	if s.bundles != nil {
		s.bundles.Put(id, b)
	}
	return summarizeBundle(id, path, b), nil
}

// OpenBundleBytes opens and validates a bundle from raw bytes (useful in tests).
func (s *Service) OpenBundleBytes(ctx context.Context, name string, data []byte) (BundleSummary, error) {
	b, err := bundle.ReadBytes(data)
	if err != nil {
		return BundleSummary{}, fmt.Errorf("open bundle bytes: %w", err)
	}
	id := generateBundleID()
	if s.bundles != nil {
		s.bundles.Put(id, b)
	}
	return summarizeBundle(id, name, b), nil
}

// GetBundle returns a previously opened bundle from cache.
func (s *Service) GetBundle(bundleID string) (*bundle.Bundle, error) {
	if s.bundles == nil {
		return nil, errors.New("bundle cache not initialized")
	}
	b, ok := s.bundles.Get(bundleID)
	if !ok {
		return nil, fmt.Errorf("bundle %q not found or expired; please reopen the bundle", bundleID)
	}
	return b, nil
}

func (s *Service) buildBundleHandoffDoc(ctx context.Context, req BundleHandoffRequest, contextFilePath string) (handoff.Doc, string, error) {
	if err := validateTarget(req.Target); err != nil {
		return handoff.Doc{}, "", err
	}
	b, err := s.GetBundle(req.BundleID)
	if err != nil {
		return handoff.Doc{}, "", err
	}
	var transcripts []model.Transcript
	if err := json.Unmarshal(b.Transcript, &transcripts); err != nil {
		return handoff.Doc{}, "", fmt.Errorf("unmarshal bundle transcript: %w", err)
	}
	if len(transcripts) == 0 {
		return handoff.Doc{}, "", errors.New("bundle contains no transcripts")
	}

	if req.CWD != "" && !filepath.IsAbs(req.CWD) {
		return handoff.Doc{}, "", errors.New("cwd must be an absolute path")
	}

	cwd := req.CWD
	if cwd == "" {
		cwd = b.Manifest.Source.CWD
	}
	if cwd != "" {
		for i := range transcripts {
			transcripts[i].Meta.CWD = cwd
		}
	}

	redactOn := req.RedactSecrets || b.Manifest.Profile == bundle.ProfileShareSafe

	opts := handoff.Options{
		TargetAgent:      req.Target,
		BudgetTokens:     resolveBudget(req.Budget),
		IncludeReasoning: req.IncludeReasoning,
		RedactSecrets:    redactOn,
		RemappedCWD:      req.CWD,
		ContextFilePath:  contextFilePath,
	}

	doc, report := handoff.Build(transcripts, opts)
	doc.Report = report
	return doc, cwd, nil
}

// BuildBundleHandoff renders a cross-agent handoff from an opened bundle.
func (s *Service) BuildBundleHandoff(ctx context.Context, req BundleHandoffRequest) (HandoffPreview, error) {
	sessionID := "bundle-" + req.BundleID
	b, _ := s.GetBundle(req.BundleID)
	if b != nil && b.Manifest.Source.ID != "" {
		sessionID = b.Manifest.Source.ID
	}

	promptFile, contextFile, err := s.handoffFiles(sessionID)
	if err != nil {
		return HandoffPreview{}, err
	}

	doc, cwd, err := s.buildBundleHandoffDoc(ctx, req, contextFile)
	if err != nil {
		return HandoffPreview{}, err
	}

	return HandoffPreview{
		PromptMarkdown: doc.PromptMarkdown,
		FullMarkdown:   doc.FullMarkdown,
		Report:         doc.Report,
		ContextFile:    contextFile,
		PromptFile:     promptFile,
		Command:        handoff.BuildLaunchCommand(req.Target, doc.PromptMarkdown, promptFile, cwd),
		PromptBytes:    len(doc.PromptMarkdown),
	}, nil
}

// BundleHandoffCommand writes the prompt and full context files (0600, pruned)
// and returns the launch command line for an opened bundle.
func (s *Service) BundleHandoffCommand(ctx context.Context, req BundleHandoffRequest) (string, error) {
	sessionID := "bundle-" + req.BundleID
	b, _ := s.GetBundle(req.BundleID)
	if b != nil && b.Manifest.Source.ID != "" {
		sessionID = b.Manifest.Source.ID
	}

	_, contextFile, err := s.handoffFiles(sessionID)
	if err != nil {
		return "", err
	}

	doc, cwd, err := s.buildBundleHandoffDoc(ctx, req, contextFile)
	if err != nil {
		return "", err
	}

	promptFile, err := s.saveHandoffFiles(sessionID, doc)
	if err != nil {
		return "", err
	}

	return handoff.BuildLaunchCommand(req.Target, doc.PromptMarkdown, promptFile, cwd), nil
}

// RenderBundleHandoff builds the self-contained full handoff document without saving it to disk.
func (s *Service) RenderBundleHandoff(ctx context.Context, req BundleHandoffRequest) (string, error) {
	doc, _, err := s.buildBundleHandoffDoc(ctx, req, "")
	if err != nil {
		return "", err
	}
	return doc.FullMarkdown, nil
}

// SaveBundleHandoff writes a self-contained full handoff document to destPath.
func (s *Service) SaveBundleHandoff(ctx context.Context, req BundleHandoffRequest, destPath string) (string, error) {
	if destPath == "" {
		return "", nil
	}
	doc, _, err := s.buildBundleHandoffDoc(ctx, req, "")
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("bundle handoff: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".handoff-save-*.tmp")
	if err != nil {
		return "", fmt.Errorf("bundle handoff: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("bundle handoff: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.WriteString(doc.FullMarkdown); err != nil {
		return "", fmt.Errorf("bundle handoff: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("bundle handoff: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("bundle handoff: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return "", fmt.Errorf("bundle handoff: rename %s -> %s: %w", tmpName, destPath, err)
	}
	return destPath, nil
}
