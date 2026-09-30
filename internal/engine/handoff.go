package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// HandoffRequest configures the creation of a cross-agent handoff document.
type HandoffRequest struct {
	Ref              model.SessionRef `json:"ref"`
	Target           model.AgentID    `json:"target"`
	Budget           int              `json:"budget"` // token budget; 0 defaults to 80k (detailed), -1 for unlimited
	IncludeReasoning bool             `json:"includeReasoning"`
	RedactSecrets    bool             `json:"redactSecrets"`
	CWD              string           `json:"cwd,omitempty"` // override or remapped cwd
}

// HandoffPreview holds the rendered handoff prompt, report, and launch info.
type HandoffPreview struct {
	PromptMarkdown string         `json:"promptMarkdown"`
	FullMarkdown   string         `json:"fullMarkdown"`
	Report         handoff.Report `json:"report"`
	ContextFile    string         `json:"contextFile"`
	PromptFile     string         `json:"promptFile"`
	Command        string         `json:"command"`
	PromptBytes    int            `json:"promptBytes"`
}

// HandoffCacheInfo describes the handoff files kept in the data directory.
type HandoffCacheInfo struct {
	Dir   string `json:"dir"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// handoffFiles returns where the prompt and full-context files for id go.
func (s *Service) handoffFiles(id string) (promptFile, contextFile string, err error) {
	if promptFile, err = handoff.PromptFilePath(s.DataDir(), id); err != nil {
		return "", "", err
	}
	if contextFile, err = handoff.ContextFilePath(s.DataDir(), id); err != nil {
		return "", "", err
	}
	return promptFile, contextFile, nil
}

// saveHandoffFiles writes the full context and the prompt for id and returns
// the prompt file the launch command points at.
func (s *Service) saveHandoffFiles(id string, doc handoff.Doc) (string, error) {
	if _, err := handoff.SaveContextFile(s.DataDir(), id, doc.FullMarkdown); err != nil {
		return "", fmt.Errorf("handoff: save context file: %w", err)
	}
	promptFile, err := handoff.SavePromptFile(s.DataDir(), id, doc.PromptMarkdown)
	if err != nil {
		return "", fmt.Errorf("handoff: save prompt file: %w", err)
	}
	return promptFile, nil
}

// HandoffCache reports the handoff files on disk.
func (s *Service) HandoffCache() HandoffCacheInfo {
	n, size := handoff.FilesUsage(s.DataDir())
	return HandoffCacheInfo{Dir: handoff.HandoffDir(s.DataDir()), Files: n, Bytes: size}
}

// ClearHandoffCache deletes the handoff files and reports what remains.
func (s *Service) ClearHandoffCache() (HandoffCacheInfo, error) {
	_, _, err := handoff.ClearFiles(s.DataDir())
	return s.HandoffCache(), err
}

func resolveBudget(b int) int {
	if b < 0 {
		return handoff.BudgetUnlimited
	}
	if b == 0 {
		return handoff.BudgetDetailed
	}
	return b
}

func validateTarget(target model.AgentID) error {
	switch target {
	case model.AgentClaude, model.AgentCodex, model.AgentOpenCode:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnknownProvider, target)
	}
}

// buildHandoffDoc resolves session metadata, collects descendants, and
// builds the handoff Doc and Report.
func (s *Service) buildHandoffDoc(ctx context.Context, req HandoffRequest, contextFilePath string) (handoff.Doc, model.SessionMeta, error) {
	if err := validateTarget(req.Target); err != nil {
		return handoff.Doc{}, model.SessionMeta{}, err
	}

	meta, ok := s.catalog.Get(req.Ref)
	if !ok {
		return handoff.Doc{}, model.SessionMeta{}, fmt.Errorf("%w: %s", ErrUnknownSession, req.Ref.Key())
	}

	if req.CWD != "" && !filepath.IsAbs(req.CWD) {
		return handoff.Doc{}, model.SessionMeta{}, errors.New("cwd must be an absolute path")
	}

	all := s.catalog.All()
	transcripts, err := handoff.Collect(ctx, meta, all, s.loadTranscript)
	if err != nil {
		return handoff.Doc{}, model.SessionMeta{}, fmt.Errorf("handoff: collect transcripts: %w", err)
	}

	opts := handoff.Options{
		TargetAgent:      req.Target,
		BudgetTokens:     resolveBudget(req.Budget),
		IncludeReasoning: req.IncludeReasoning,
		RedactSecrets:    req.RedactSecrets,
		RemappedCWD:      req.CWD,
		ContextFilePath:  contextFilePath,
	}

	doc, report := handoff.Build(transcripts, opts)
	doc.Report = report
	return doc, meta, nil
}

// BuildHandoff renders the handoff without modifying any files on disk. The
// command it shows points at the prompt file HandoffCommand will write.
func (s *Service) BuildHandoff(ctx context.Context, req HandoffRequest) (HandoffPreview, error) {
	promptFile, contextFile, err := s.handoffFiles(req.Ref.ID)
	if err != nil {
		return HandoffPreview{}, err
	}

	doc, meta, err := s.buildHandoffDoc(ctx, req, contextFile)
	if err != nil {
		return HandoffPreview{}, err
	}

	cwd := req.CWD
	if cwd == "" {
		cwd = meta.CWD
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

// HandoffCommand writes the prompt and full context files (0600, pruned) and
// returns the launch command line, which points at the prompt file.
func (s *Service) HandoffCommand(ctx context.Context, req HandoffRequest) (string, error) {
	_, contextFile, err := s.handoffFiles(req.Ref.ID)
	if err != nil {
		return "", err
	}

	doc, meta, err := s.buildHandoffDoc(ctx, req, contextFile)
	if err != nil {
		return "", err
	}

	promptFile, err := s.saveHandoffFiles(req.Ref.ID, doc)
	if err != nil {
		return "", err
	}

	cwd := req.CWD
	if cwd == "" {
		cwd = meta.CWD
	}

	return handoff.BuildLaunchCommand(req.Target, doc.PromptMarkdown, promptFile, cwd), nil
}

// RenderHandoff builds the self-contained full handoff document without saving it to disk.
func (s *Service) RenderHandoff(ctx context.Context, req HandoffRequest) (string, error) {
	doc, _, err := s.buildHandoffDoc(ctx, req, "")
	if err != nil {
		return "", err
	}
	return doc.FullMarkdown, nil
}

// SaveHandoff writes a self-contained full handoff document (with no context file
// reference) to destPath with 0600 permissions.
func (s *Service) SaveHandoff(ctx context.Context, req HandoffRequest, destPath string) (string, error) {
	if destPath == "" {
		return "", nil
	}
	doc, _, err := s.buildHandoffDoc(ctx, req, "")
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("handoff: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".handoff-save-*.tmp")
	if err != nil {
		return "", fmt.Errorf("handoff: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("handoff: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.WriteString(doc.FullMarkdown); err != nil {
		return "", fmt.Errorf("handoff: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("handoff: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("handoff: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return "", fmt.Errorf("handoff: rename %s -> %s: %w", tmpName, destPath, err)
	}

	return destPath, nil
}
