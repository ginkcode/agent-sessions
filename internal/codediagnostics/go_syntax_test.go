package codediagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syntaxRoot(t *testing.T, files map[string]string) (AuthorizedRoot, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := AuthorizeRoot("local:test", dir, func(s string) bool { return s == dir })
	if err != nil {
		t.Fatal(err)
	}
	return root, root.Project().Root
}

func TestGoSyntaxFindsSavedErrorsWithUTF16Ranges(t *testing.T) {
	bad := "package p\n\n// 😀\nvar s = \"é\" +\nfunc\n"
	root, _ := syntaxRoot(t, map[string]string{
		"ok.go":                 "package p\n",
		"sub/bad.go":            bad,
		"node_modules/x/bad.go": "nope",
		"vendor/v/bad.go":       "nope",
		".hidden/bad.go":        "nope",
		"testdata/bad.go":       "nope",
		"_ignored.go":           "nope",
		"notes.txt":             "not go",
	})
	g := NewGoSyntax(root, GoSyntaxLimits{})
	r, _, changes, err := g.Full()
	if err != nil {
		t.Fatal(err)
	}
	if !r.Replace || r.Phase != "idle" || len(changes) != 2 {
		t.Fatalf("result %+v changes %v", r, changes)
	}
	if !r.Partial {
		t.Fatal("skipping dependency trees must report partial coverage")
	}
	if len(r.Diagnostics) == 0 {
		t.Fatal("expected a syntax error")
	}
	sum := sha256.Sum256([]byte(bad))
	for _, d := range r.Diagnostics {
		if d.Path != "sub/bad.go" || d.Severity != "error" || d.Freshness != "current" || d.Basis.Revision != "sha256:"+hex.EncodeToString(sum[:]) || d.Range == nil {
			t.Fatalf("diagnostic %+v", d)
		}
		if !rangeInText(bad, *d.Range) {
			t.Fatalf("range %+v not valid UTF-16 for text", d.Range)
		}
	}
}

func TestGoSyntaxUpdateCreateFixDelete(t *testing.T) {
	root, dir := syntaxRoot(t, map[string]string{"a.go": "package a\nfunc {"})
	g := NewGoSyntax(root, GoSyntaxLimits{})
	r, _, _, err := g.Full()
	if err != nil || len(r.Diagnostics) == 0 {
		t.Fatalf("initial %+v %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\nvar = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, changes, err := g.Update([]string{"a.go", "b.go", "c.txt", "../escape.go"})
	if err != nil || len(changes) != 2 || changes[0] != (FileChange{"a.go", 2}) || changes[1] != (FileChange{"b.go", 1}) {
		t.Fatalf("update %v %v", changes, err)
	}
	for _, d := range r.Diagnostics {
		if d.Path != "b.go" {
			t.Fatalf("fixed file still reported: %+v", d)
		}
	}
	if len(r.Diagnostics) == 0 {
		t.Fatal("created file error missing")
	}
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	r, changes, _ = g.Update([]string{"b.go"})
	if len(r.Diagnostics) != 0 || len(changes) != 1 || changes[0].Kind != 3 {
		t.Fatalf("delete %+v %v", r.Diagnostics, changes)
	}
}

func TestGoSyntaxLimitsArePartialNeverClean(t *testing.T) {
	files := map[string]string{"big.go": "package p\n" + strings.Repeat("// x\n", 400) + "func {"}
	for i := range 5 {
		files[filepath.ToSlash(filepath.Join("d", string(rune('a'+i))+".go"))] = "package d\n"
	}
	root, _ := syntaxRoot(t, files)
	r, _, _, _ := NewGoSyntax(root, GoSyntaxLimits{FileBytes: 64}).Full()
	if !r.Partial || len(r.Diagnostics) != 0 {
		t.Fatalf("oversized file must be partial, not clean: %+v", r)
	}
	r, _, _, _ = NewGoSyntax(root, GoSyntaxLimits{Files: 2}).Full()
	if !r.Partial {
		t.Fatal("file cap must report partial")
	}
	r, _, _, _ = NewGoSyntax(root, GoSyntaxLimits{Entries: 2}).Full()
	if !r.Partial {
		t.Fatal("entry cap must report partial")
	}
	r, _, _, _ = NewGoSyntax(root, GoSyntaxLimits{PerFile: 1}).Full()
	if len(r.Diagnostics) > 1 {
		t.Fatal("per-file cap ignored")
	}
}

func TestGoSyntaxNeverFollowsEscapingSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.go"), []byte("func {"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, dir := syntaxRoot(t, map[string]string{"ok.go": "package p\n"})
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.go"), filepath.Join(dir, "file.go")); err != nil {
		t.Fatal(err)
	}
	g := NewGoSyntax(root, GoSyntaxLimits{})
	r, dirs, _, err := g.Full()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Diagnostics) != 0 || !r.Partial {
		t.Fatalf("escaping link analyzed: %+v", r)
	}
	for _, d := range dirs {
		if d.rel == "linked" {
			t.Fatal("symlinked directory walked")
		}
	}
	if _, changes, _ := g.Update([]string{"linked/x.go"}); len(changes) != 0 {
		t.Fatal("update followed an escaping link")
	}
}

func TestInProcessPublicationRequiresCurrentApproval(t *testing.T) {
	root, tools, _ := fixture(t)
	trust := NewTrust(nil)
	m, err := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	f := &fakeChecker{tools, make(chan adapterCall, 1)}
	s, err := m.Attach(root, 1, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}, {ProviderID: GoSyntaxProviderID, Availability: "available", InProcess: true}})
	if err != nil {
		t.Fatal(err)
	}
	result := NewGoSyntax(root, GoSyntaxLimits{}).result()
	if m.InProcessApproved(s.Context, GoSyntaxProviderID) || m.PublishInProcess(s.Context, GoSyntaxProviderID, result, "watching") == nil {
		t.Fatal("published before approval")
	}
	for _, p := range s.Proposal.Tools {
		if p.ProviderID == GoSyntaxProviderID {
			t.Fatal("in-process provider must not add an executable to the proposal")
		}
	}
	s, err = m.Approve(s.Context, s.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.PublishInProcess(s.Context, "checker", result, "watching"); err == nil {
		t.Fatal("executable provider accepted in-process results")
	}
	r, _, _, _ := NewGoSyntax(root, GoSyntaxLimits{}).Full()
	if err := m.PublishInProcess(s.Context, GoSyntaxProviderID, r, "watching"); err != nil {
		t.Fatal(err)
	}
	s, _ = m.GetSnapshot(s.Context)
	p := provider(&attachment{snapshot: s}, GoSyntaxProviderID)
	if p.Coverage.ExternalChanges != "watching" || p.Coverage.Kind != "savedProject" || len(s.ResultSets) != 1 || s.ResultSets[0].Origin != "checker" || s.ResultSets[0].RunID == "" || len(s.ResultSets[0].Diagnostics) == 0 {
		t.Fatalf("snapshot %+v", s)
	}
	if runs, err := m.Run(s.Context, []string{GoSyntaxProviderID}, false); err != nil || len(runs) != 0 {
		t.Fatalf("on-demand run of in-process provider: %v %v", runs, err)
	}
	old := s.Context
	s, _ = m.Revoke(s.Context)
	if len(s.ResultSets) != 0 || m.PublishInProcess(old, GoSyntaxProviderID, r, "watching") == nil || m.PublishInProcess(s.Context, GoSyntaxProviderID, r, "watching") == nil {
		t.Fatal("late publication after revoke")
	}
	if provider(&attachment{snapshot: s}, GoSyntaxProviderID).Coverage.ExternalChanges != "unwatched" {
		t.Fatal("revoked provider still claims watching")
	}
}

// Regression (2026-10-23 app report): with no executable checker discovered
// (e.g. a Finder-launched app whose PATH lacks the Go toolchain) the approval
// buttons were disabled because no proposal existed. The built-in analyzer now
// gets its own explicit analyzer-only proposal; approving it grants no
// executable, and an unreviewed in-process ID is never offered.
func TestInProcessAnalyzerOnlyProposal(t *testing.T) {
	root, tools, _ := fixture(t)
	trust := NewTrust(store(t))
	m, _ := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	defer m.Close()
	s, err := m.Attach(root, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Proposal != nil {
		t.Fatal("proposal before discovery")
	}
	regs := []Registration{{ProviderID: "go-vet", Availability: "missingTool"}, {ProviderID: GoSyntaxProviderID, Availability: "available", InProcess: true}, {ProviderID: "unreviewed", Availability: "available", InProcess: true}}
	if err := m.CompleteDiscovery(s.Context, regs); err != nil {
		t.Fatal(err)
	}
	s, _ = m.GetSnapshot(s.Context)
	syntax := func(s Snapshot) ProviderState { return *provider(&attachment{snapshot: s}, GoSyntaxProviderID) }
	if s.Proposal == nil || !validFingerprint(s.Proposal.Fingerprint) || len(s.Proposal.Tools) != 0 || len(s.Proposal.Analyzers) != 1 || s.Proposal.Analyzers[0].ProviderID != GoSyntaxProviderID || s.Proposal.Analyzers[0].Description == "" {
		t.Fatalf("analyzer-only proposal: %+v", s.Proposal)
	}
	if syntax(s).Availability != "approvalRequired" || provider(&attachment{snapshot: s}, "unreviewed").Availability != "unsupported" || s.Approval.State != "required" {
		t.Fatalf("providers %+v", s.Providers)
	}
	if m.InProcessApproved(s.Context, GoSyntaxProviderID) {
		t.Fatal("approved before consent")
	}
	if _, err := m.Approve(s.Context, "sha256-v1:"+strings.Repeat("0", 64), "session"); err == nil {
		t.Fatal("stale fingerprint accepted")
	}
	// The analyzer identity is a distinct payload kind; it can never equal a
	// tool fingerprint, and it does not authorize any executable.
	if _, err := trust.Approve(root, tools, s.Proposal.Fingerprint, "session"); err == nil {
		t.Fatal("analyzer identity approved an executable")
	}
	fp := s.Proposal.Fingerprint
	s, err = m.Approve(s.Context, fp, "persistent")
	if err != nil || s.Approval.State != "approved" || syntax(s).Availability != "available" || !m.InProcessApproved(s.Context, GoSyntaxProviderID) {
		t.Fatalf("approve: %v %+v", err, s)
	}
	if _, err := trust.Preflight(root, nil); err == nil {
		t.Fatal("analyzer-only grant produced an executable lease")
	}
	if _, err := m.Run(s.Context, nil, false); err == nil {
		t.Fatal("run with no executable checker")
	}
	if runs, err := m.Run(s.Context, []string{GoSyntaxProviderID}, false); err != nil || len(runs) != 0 {
		t.Fatalf("in-process run: %v %v", runs, err)
	}
	// Persistent: a fresh attachment of the same project is already approved.
	if err := m.Detach(s.Context); err != nil {
		t.Fatal(err)
	}
	s, err = m.Attach(root, 1, regs)
	if err != nil || s.Approval.State != "approved" || s.Approval.Mode != "persistent" || !m.InProcessApproved(s.Context, GoSyntaxProviderID) {
		t.Fatalf("persistent reattach: %v %+v", err, s)
	}
	// Adding an executable checker changes the identity: renewed consent.
	if err := m.Detach(s.Context); err != nil {
		t.Fatal(err)
	}
	s, err = m.Attach(root, 1, append(regs[1:2:2], Registration{ProviderID: "checker", Availability: "available", Adapter: &fakeChecker{tools, make(chan adapterCall, 1)}}))
	if err != nil || s.Approval.State != "required" || s.Proposal.Fingerprint == fp || len(s.Proposal.Tools) != 1 || len(s.Proposal.Analyzers) != 1 {
		t.Fatalf("composite: %v %+v", err, s)
	}
	// Revoke clears the analyzer grant too.
	if _, err := m.Revoke(s.Context); err != nil {
		t.Fatal(err)
	}
	if err := m.Detach(s.Context); err == nil {
		t.Fatal("revoked generation still current")
	}
}

func TestInProcessWithoutReviewedAnalyzerIsUnsupported(t *testing.T) {
	root, _, _ := fixture(t)
	trust := NewTrust(nil)
	m, _ := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	defer m.Close()
	s, err := m.Attach(root, 1, []Registration{{ProviderID: "unreviewed", Availability: "available", InProcess: true}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Proposal != nil || s.Providers[0].Availability != "unsupported" || m.InProcessApproved(s.Context, "unreviewed") {
		t.Fatalf("no proposal must not enable watching: %+v", s.Providers[0])
	}
}
