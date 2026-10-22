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

func TestInProcessWithoutProposalIsUnsupported(t *testing.T) {
	root, _, _ := fixture(t)
	trust := NewTrust(nil)
	m, _ := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	defer m.Close()
	s, err := m.Attach(root, 1, []Registration{{ProviderID: GoSyntaxProviderID, Availability: "available", InProcess: true}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Providers[0].Availability != "unsupported" || m.InProcessApproved(s.Context, GoSyntaxProviderID) {
		t.Fatalf("no proposal must not enable watching: %+v", s.Providers[0])
	}
}
