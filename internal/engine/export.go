package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/handoff"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/redact"
	"github.com/ginkcode/agent-sessions/internal/version"
)

// ExportRequest configures a bundle export.
type ExportRequest struct {
	Ref              model.SessionRef `json:"ref"`
	Profile          string           `json:"profile"` // "complete" (default) or "share-safe"
	Budget           int              `json:"budget"`  // handoff token budget; 0 = detailed, -1 = unlimited
	IncludeReasoning bool             `json:"includeReasoning"`
	// RedactSecrets scrubs the transcript and handoff. Share-safe forces it on.
	RedactSecrets bool `json:"redactSecrets"`
}

// ExportPreview estimates an export without writing anything.
type ExportPreview struct {
	Profile       string                `json:"profile"`
	Sessions      int                   `json:"sessions"`
	NativeFiles   int                   `json:"nativeFiles"`
	NativeBytes   int64                 `json:"nativeBytes"`
	Redaction     redact.Counts         `json:"redaction"`
	Fidelity      bundle.FidelityReport `json:"fidelity"`
	HandoffTokens int                   `json:"handoffTokens"`
	// Warning is the sensitive-data notice for the complete profile.
	Warning string `json:"warning,omitempty"`
}

// completeWarning is the exact notice the export dialog shows for a
// restorable bundle: native records can carry secrets.
const completeWarning = "A complete bundle contains the session's original records and can include secrets such as tokens, keys, and passwords."

// PreviewExport reports what an export would contain. It reads transcripts
// and native records but writes nothing.
func (s *Service) PreviewExport(ctx context.Context, req ExportRequest, mgr *manage.Manager) (ExportPreview, error) {
	built, err := s.prepareExport(ctx, req, mgr)
	if err != nil {
		return ExportPreview{}, err
	}
	return built.preview(), nil
}

// ExportBundle writes the bundle to destPath. An empty destPath means the
// caller cancelled, and nothing is written.
func (s *Service) ExportBundle(ctx context.Context, req ExportRequest, destPath string, mgr *manage.Manager) (string, error) {
	if destPath == "" {
		return "", nil
	}
	built, err := s.prepareExport(ctx, req, mgr)
	if err != nil {
		return "", err
	}
	if err := bundle.WriteToFile(destPath, built.request()); err != nil {
		return "", err
	}
	return destPath, nil
}

// exportBuild is one resolved export, shared by the preview and the writer so
// the two cannot drift.
type exportBuild struct {
	req       ExportRequest
	meta      model.SessionMeta
	profile   bundle.Profile
	sessions  []bundle.SessionInput
	nativeN   int
	nativeB   int64
	handoff   string
	report    handoff.Report
	fidelity  bundle.FidelityReport
	redaction redact.Counts
	body      []byte
}

func (b exportBuild) preview() ExportPreview {
	p := ExportPreview{
		Profile:       string(b.profile),
		Sessions:      len(b.sessions),
		NativeFiles:   b.nativeN,
		NativeBytes:   b.nativeB,
		Redaction:     b.redaction,
		Fidelity:      b.fidelity,
		HandoffTokens: b.report.EstimatedTokens,
	}
	if b.profile == bundle.ProfileComplete {
		p.Warning = completeWarning
	}
	return p
}

func (b exportBuild) request() bundle.WriteRequest {
	return bundle.WriteRequest{
		AppVersion: version.Current(),
		Profile:    b.profile,
		Source: bundle.SourceMeta{
			Agent:        b.meta.Ref.Agent,
			AgentVersion: b.meta.AgentVersion,
			ID:           b.meta.Ref.ID,
			CWD:          b.meta.CWD,
			RepoRoot:     b.meta.RepoRoot,
			GitBranch:    b.meta.GitBranch,
			Title:        b.meta.Title,
		},
		Sessions:   b.sessions,
		Redaction:  bundle.RedactionManifest{Rules: b.redaction.Map(), Counts: b.redaction.Total()},
		Handoff:    b.handoff,
		Transcript: b.body,
	}
}

// prepareExport loads the session, restores full fidelity, renders the
// handoff, and (for a complete bundle) captures native records.
func (s *Service) prepareExport(ctx context.Context, req ExportRequest, mgr *manage.Manager) (exportBuild, error) {
	profile, err := parseProfile(req.Profile)
	if err != nil {
		return exportBuild{}, err
	}
	meta, all, err := s.exportSessions(ctx, req.Ref)
	if err != nil {
		return exportBuild{}, err
	}
	transcripts, err := handoff.Collect(ctx, meta, all, s.loadTranscript)
	if err != nil {
		return exportBuild{}, fmt.Errorf("export: collect transcripts: %w", err)
	}
	if len(transcripts) == 0 {
		return exportBuild{}, errors.New("export: session has no transcript")
	}

	prov, ok := s.providers.Get(req.Ref.Agent)
	if !ok {
		return exportBuild{}, fmt.Errorf("%w: %s", ErrUnknownProvider, req.Ref.Agent)
	}
	resolved, fidelity, err := bundle.ResolveFidelity(ctx, transcripts, prov.Blob)
	if err != nil {
		return exportBuild{}, fmt.Errorf("export: resolve transcript: %w", err)
	}

	// The handoff is rendered from the resolved transcripts, so its tool
	// outputs are complete too. Redaction happens afterwards and the counts
	// cover both artifacts.
	redactOn := req.RedactSecrets || profile == bundle.ProfileShareSafe
	doc, report := handoff.Build(resolved, handoff.Options{
		TargetAgent:      model.AgentClaude,
		BudgetTokens:     resolveBudget(req.Budget),
		IncludeReasoning: req.IncludeReasoning,
		RedactSecrets:    redactOn,
	})

	var counts redact.Counts
	if redactOn {
		for i := range resolved {
			counts = counts.Add(redact.Transcript(&resolved[i]))
		}
		counts = counts.Add(report.RedactionCounts)
	}

	sessions, nativeN, nativeB, err := nativeSessions(ctx, profile, meta, all, resolved, mgr)
	if err != nil {
		return exportBuild{}, err
	}
	body, err := json.Marshal(resolved)
	if err != nil {
		return exportBuild{}, fmt.Errorf("export: encode transcript: %w", err)
	}
	return exportBuild{
		req: req, meta: meta, profile: profile, sessions: sessions,
		nativeN: nativeN, nativeB: nativeB, handoff: doc.FullMarkdown,
		report: report, fidelity: fidelity, redaction: counts, body: body,
	}, nil
}

// nativeSessions captures verbatim records for a complete bundle. Share-safe
// bundles list the sessions with no native entries.
func nativeSessions(ctx context.Context, profile bundle.Profile, meta model.SessionMeta, all []model.SessionMeta, transcripts []model.Transcript, mgr *manage.Manager) ([]bundle.SessionInput, int, int64, error) {
	order := make([]model.SessionRef, 0, len(transcripts))
	parents := make(map[string]string, len(transcripts))
	for _, tr := range transcripts {
		order = append(order, tr.Meta.Ref)
		parents[tr.Meta.Ref.Key()] = tr.Meta.ParentID
	}
	if profile == bundle.ProfileShareSafe {
		inputs, err := sessionInputs(order, parents, nil)
		return inputs, 0, 0, err
	}
	if mgr == nil {
		return nil, 0, 0, errors.New("export: native capture is unavailable")
	}
	captured, err := mgr.CaptureSession(ctx, meta, all)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("export: capture native records: %w", err)
	}
	byKey := make(map[string]manage.Capture, len(captured))
	for _, c := range captured {
		byKey[c.Ref.Key()] = c
	}
	inputs, err := sessionInputs(order, parents, byKey)
	if err != nil {
		return nil, 0, 0, err
	}
	var n int
	var size int64
	for _, s := range inputs {
		n += len(s.Native)
		for _, f := range s.Native {
			size += int64(len(f.Data))
		}
	}
	return inputs, n, size, nil
}

// sessionInputs builds one SessionInput per transcript, in transcript order.
// Captures the transcript walk did not produce a session for are appended, so
// a descendant that failed to load is still represented by its native record.
func sessionInputs(order []model.SessionRef, parents map[string]string, captured map[string]manage.Capture) ([]bundle.SessionInput, error) {
	out := make([]bundle.SessionInput, 0, len(order))
	seen := make(map[string]bool, len(order))
	var failed error
	add := func(ref model.SessionRef, parent string) {
		if seen[ref.Key()] {
			return
		}
		seen[ref.Key()] = true
		in := bundle.SessionInput{Agent: string(ref.Agent), ID: ref.ID, ParentID: parent}
		if c, ok := captured[ref.Key()]; ok {
			native, err := nativeInputs(string(ref.Agent), c)
			if err != nil {
				failed = err
				return
			}
			in.Native = native
		}
		out = append(out, in)
	}
	for _, ref := range order {
		add(ref, parents[ref.Key()])
	}
	for key, c := range captured {
		if !seen[key] {
			add(c.Ref, c.ParentID)
		}
	}
	if failed != nil {
		return nil, failed
	}
	return out, nil
}

// nativeInputs reads the captured files. OpenCode has no files: its export
// payload is the CLI's stdout, stored under export/<id>.json.
func nativeInputs(agent string, c manage.Capture) ([]bundle.NativeInput, error) {
	var out []bundle.NativeInput
	for _, f := range c.Files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return nil, fmt.Errorf("export: read %s: %w", f.Rel, err)
		}
		out = append(out, bundle.NativeInput{Agent: agent, Rel: f.Rel, Data: data})
	}
	if len(c.Body) > 0 {
		out = append(out, bundle.NativeInput{Agent: agent, Rel: "export/" + c.Ref.ID + ".json", Data: c.Body})
	}
	return out, nil
}

// exportSessions prefers the catalog, which carries descendants the scanner
// found. When the catalog has no entry, the provider is asked directly so a
// CLI export works from a plain Load without a prior scan.
func (s *Service) exportSessions(ctx context.Context, ref model.SessionRef) (model.SessionMeta, []model.SessionMeta, error) {
	if meta, ok := s.catalog.Get(ref); ok {
		return meta, s.catalog.All(), nil
	}
	tr, err := s.loadTranscript(ctx, ref)
	if err != nil {
		return model.SessionMeta{}, nil, fmt.Errorf("export: load %s: %w", ref.Key(), err)
	}
	if tr == nil {
		return model.SessionMeta{}, nil, fmt.Errorf("%w: %s", ErrUnknownSession, ref.Key())
	}
	return tr.Meta, []model.SessionMeta{tr.Meta}, nil
}

func parseProfile(s string) (bundle.Profile, error) {
	switch strings.TrimSpace(s) {
	case "", string(bundle.ProfileComplete):
		return bundle.ProfileComplete, nil
	case string(bundle.ProfileShareSafe):
		return bundle.ProfileShareSafe, nil
	default:
		return "", fmt.Errorf("export: unknown profile %q", s)
	}
}

// ExportFileName returns the standard sanitized filename for a session
// bundle: <agent>_<directory name>_<short id>.agent-session.zip, so bundles
// saved side by side say where they came from. Unknown parts are left out.
func ExportFileName(meta model.SessionMeta) string {
	id := shortID(meta.Ref.ID)
	if id == "" {
		id = "session"
	}
	var parts []string
	for _, p := range []string{string(meta.Ref.Agent), dirName(meta.CWD), id} {
		if p = fileNamePart(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "_") + ".agent-session.zip"
}

// shortIDMax caps IDs that have no "-" block, such as O‍penCode's
// ses_<base62> IDs.
const shortIDMax = 12

// shortID returns the first block of id: the part before the first "-" or
// "/", which is the first 8 hex digits of a UUID.
func shortID(id string) string {
	if i := strings.IndexAny(id, "-/"); i > 0 {
		id = id[:i]
	}
	if len(id) > shortIDMax {
		id = id[:shortIDMax]
	}
	return id
}

// dirName returns the last element of cwd, which may be a POSIX path from a
// remote host or a Windows path.
func dirName(cwd string) string {
	cwd = strings.TrimRight(cwd, `/\`)
	if i := strings.LastIndexAny(cwd, `/\`); i >= 0 {
		cwd = cwd[i+1:]
	}
	return cwd
}

// fileNamePart replaces characters that are unsafe in file names on any
// platform, and trims the dots and dashes left at either end.
func fileNamePart(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', ' ', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	return strings.Trim(s, ".-")
}
