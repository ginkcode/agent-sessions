package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/files"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func editRevision(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeEditFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readEditFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func editEngine(dirs ...string) *Engine {
	catalog := scan.NewCatalog()
	var metas []model.SessionMeta
	for i, d := range dirs {
		metas = append(metas, model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: string(rune('a' + i))}, CWD: d})
	}
	catalog.Reset(metas)
	return &Engine{svc: NewService(catalog, nil)}
}

func TestSaveFileRejectsUnknownDirectory(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	e := editEngine(root)
	ctx := context.Background()
	const body = "original\n"
	writeEditFile(t, root, "sub/file.txt", body)
	otherFile := writeEditFile(t, other, "file.txt", body)

	for _, tc := range []struct{ name, dir, rel string }{
		{"empty", "", "file.txt"},
		{"unrelated", other, "file.txt"},
		{"subdirectory of session dir", filepath.Join(root, "sub"), "file.txt"},
		{"parent of session dir", filepath.Dir(root), filepath.Join(filepath.Base(root), "sub", "file.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.SaveFile(ctx, tc.dir, files.SaveRequest{Path: tc.rel, Text: "changed\n", ExpectedRevision: editRevision(body)})
			if !errors.Is(err, ErrUnknownDirectory) {
				t.Fatalf("SaveFile err = %v, want ErrUnknownDirectory", err)
			}
			if res.Status != "" || res.Content != nil {
				t.Fatalf("result = %+v, want zero", res)
			}
			if _, err := e.ReadFile(ctx, tc.dir, tc.rel); !errors.Is(err, ErrUnknownDirectory) {
				t.Fatalf("ReadFile err = %v, want ErrUnknownDirectory", err)
			}
		})
	}
	if got := readEditFile(t, otherFile); got != body {
		t.Fatalf("unrelated file changed: %q", got)
	}
	if got := readEditFile(t, filepath.Join(root, "sub", "file.txt")); got != body {
		t.Fatalf("session file changed: %q", got)
	}

	// The known directory itself still saves.
	res, err := e.SaveFile(ctx, root, files.SaveRequest{Path: "sub/file.txt", Text: "changed\n", ExpectedRevision: editRevision(body)})
	if err != nil || res.Status != files.SaveStatusSaved || res.Content == nil || res.Content.Text != "changed\n" {
		t.Fatalf("known dir save = %+v, %v", res, err)
	}
}

// The engine protects its configured roots, its cache override and the
// default roots of this user at once, so a partly configured engine still
// protects the usual stores.
func TestSaveFileProtectsConfiguredAndDefaultRoots(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	// Default roots resolve from this environment: CLAUDE_CONFIG_DIR is
	// honored on every OS, and the OpenCode store falls under home.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude-custom"))
	defaults, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	openCodeRel, err := filepath.Rel(root, defaults.OpenCodeData)
	if err != nil || !filepath.IsLocal(openCodeRel) {
		t.Fatalf("default OpenCode root %q is not under %q", defaults.OpenCodeData, root)
	}

	e := editEngine(root)
	e.roots = paths.Roots{Codex: filepath.Join(root, "configured-codex"), Config: filepath.Join(root, "configured-config")}
	e.cacheDirOverride = filepath.Join(root, "cache-override")
	ctx := context.Background()
	const body = "original\n"

	for _, tc := range []struct{ name, rel string }{
		{"configured tool root", "configured-codex/notes.txt"},
		{"configured app root", "configured-config/notes.txt"},
		{"cache override", "cache-override/notes.txt"},
		{"default root from env", "claude-custom/notes.txt"},
		{"default root from home", filepath.ToSlash(filepath.Join(openCodeRel, "notes.txt"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeEditFile(t, root, tc.rel, body)
			c, err := e.ReadFile(ctx, root, tc.rel)
			if err != nil {
				t.Fatal(err)
			}
			if c.Edit == nil || c.Edit.Eligible || c.Edit.Reason != files.EditProtected {
				t.Fatalf("read edit = %+v, want protected", c.Edit)
			}
			res, err := e.SaveFile(ctx, root, files.SaveRequest{Path: tc.rel, Text: "changed\n", ExpectedRevision: c.Revision})
			if !errors.Is(err, files.ErrEditProtected) {
				t.Fatalf("SaveFile = %+v, %v; want ErrEditProtected", res, err)
			}
			if got := readEditFile(t, p); got != body {
				t.Fatalf("protected file changed: %q", got)
			}
		})
	}

	// A sibling outside every protected root stays editable.
	p := writeEditFile(t, root, "project/notes.txt", body)
	c, err := e.ReadFile(ctx, root, "project/notes.txt")
	if err != nil || c.Edit == nil || !c.Edit.Eligible {
		t.Fatalf("free read = %+v, %v", c.Edit, err)
	}
	res, err := e.SaveFile(ctx, root, files.SaveRequest{Path: "project/notes.txt", Text: "changed\n", ExpectedRevision: c.Revision})
	if err != nil || res.Status != files.SaveStatusSaved {
		t.Fatalf("free save = %+v, %v", res, err)
	}
	if got := readEditFile(t, p); got != "changed\n" {
		t.Fatalf("free file = %q", got)
	}
}

func TestEditPolicyRoots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "default-claude"))
	defaults, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	configured := paths.Roots{
		Claude: filepath.Join(root, "claude"),
		// Relative and duplicate roots are dropped.
		Codex: "relative/codex",
		Cache: defaults.Cache,
	}
	e := &Engine{roots: configured, cacheDirOverride: filepath.Join(root, "cache")}
	got := map[string]int{}
	for _, r := range e.editPolicy().ProtectedRoots {
		if !filepath.IsAbs(r) {
			t.Errorf("relative protected root %q", r)
		}
		got[r]++
	}
	for _, want := range []string{
		configured.Claude, e.cacheDirOverride,
		defaults.Claude, defaults.Codex, defaults.OpenCodeData, defaults.Cache, defaults.Config, defaults.Data,
	} {
		if got[want] == 0 {
			t.Errorf("missing protected root %q in %v", want, got)
		}
	}
	for r, n := range got {
		if n > 1 {
			t.Errorf("protected root %q listed %d times", r, n)
		}
	}
	if got["relative/codex"] != 0 {
		t.Errorf("relative configured root kept")
	}

	// A relative cache override is ignored rather than protecting a path
	// relative to the process directory.
	e.cacheDirOverride = "relative-cache"
	for _, r := range e.editPolicy().ProtectedRoots {
		if r == "relative-cache" {
			t.Fatal("relative cache override kept")
		}
	}
}
