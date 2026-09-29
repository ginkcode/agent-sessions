package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

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
	Command        string         `json:"command"`
	FilePointer    bool           `json:"filePointer"`
	PromptBytes    int            `json:"promptBytes"`
}

// SetDataDir overrides the data directory used for handoff context files.
func (s *Service) SetDataDir(dir string) {
	s.dataDir = dir
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

// BuildHandoff renders the handoff without modifying any files on disk.
func (s *Service) BuildHandoff(ctx context.Context, req HandoffRequest) (HandoffPreview, error) {
	contextFile, err := handoff.ContextFilePath(s.DataDir(), req.Ref.ID)
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

	cmd := handoff.BuildLaunchCommand(req.Target, doc.PromptMarkdown, contextFile, cwd)
	_, filePointer := handoff.LaunchPrompt(doc.PromptMarkdown, contextFile)

	return HandoffPreview{
		PromptMarkdown: doc.PromptMarkdown,
		FullMarkdown:   doc.FullMarkdown,
		Report:         doc.Report,
		ContextFile:    contextFile,
		Command:        cmd,
		FilePointer:    filePointer,
		PromptBytes:    len(doc.PromptMarkdown),
	}, nil
}

// HandoffCommand writes the full context file to disk (0600, pruned) and returns
// the launch command line.
func (s *Service) HandoffCommand(ctx context.Context, req HandoffRequest) (string, error) {
	contextFile, err := handoff.ContextFilePath(s.DataDir(), req.Ref.ID)
	if err != nil {
		return "", err
	}

	doc, meta, err := s.buildHandoffDoc(ctx, req, contextFile)
	if err != nil {
		return "", err
	}

	savedFile, err := handoff.SaveContextFile(s.DataDir(), req.Ref.ID, doc.FullMarkdown)
	if err != nil {
		return "", fmt.Errorf("handoff: save context file: %w", err)
	}

	cwd := req.CWD
	if cwd == "" {
		cwd = meta.CWD
	}

	return handoff.BuildLaunchCommand(req.Target, doc.PromptMarkdown, savedFile, cwd), nil
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

// BuildHandoff renders the handoff document preview for the desktop frontend.
func (a *App) BuildHandoff(req HandoffRequest) (HandoffPreview, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.svc.BuildHandoff(ctx, req)
}

// HandoffCommand generates the launch command and writes the full context file.
func (a *App) HandoffCommand(req HandoffRequest) (string, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.svc.HandoffCommand(ctx, req)
}

// SaveHandoff opens a file save dialog and saves the self-contained full handoff
// document to the chosen file. If the dialog is cancelled, it returns an empty path.
func (a *App) SaveHandoff(req HandoffRequest) (string, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	defaultName := "handoff.md"
	if req.Ref.ID != "" {
		defaultName = req.Ref.ID + "-handoff.md"
	}

	var destPath string
	var err error
	if a.saveDialogOverride != nil {
		destPath, err = a.saveDialogOverride(ctx, defaultName)
	} else {
		destPath, err = wruntime.SaveFileDialog(ctx, wruntime.SaveDialogOptions{
			Title:           "Save Handoff Document",
			DefaultFilename: defaultName,
			Filters: []wruntime.FileFilter{
				{DisplayName: "Markdown Files (*.md)", Pattern: "*.md"},
				{DisplayName: "All Files (*.*)", Pattern: "*.*"},
			},
		})
	}
	if err != nil {
		return "", fmt.Errorf("handoff: save file dialog: %w", err)
	}
	if destPath == "" {
		return "", nil
	}

	return a.svc.SaveHandoff(ctx, req, destPath)
}
