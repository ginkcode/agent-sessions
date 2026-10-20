package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func editPathFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
}

func editPathSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestEditPolicyPaths(t *testing.T) {
	root := t.TempDir()
	editPathFile(t, filepath.Join(root, "dir", "file.txt"))
	for _, tc := range []struct {
		name string
		rel  string
		want error
	}{
		{"regular", "dir/file.txt", nil},
		{"parent component", "dir/../dir/file.txt", ErrOutsideRoot},
		{"single dot", "./dir/./file.txt", nil},
		{"empty", "", ErrOutsideRoot},
		{"dot", ".", ErrOutsideRoot},
		{"cleaned dot", "dir/..", ErrOutsideRoot},
		{"parent", "../file.txt", ErrOutsideRoot},
		{"nested escape", "dir/../../file.txt", ErrOutsideRoot},
		{"absolute", filepath.Join(root, "dir", "file.txt"), ErrOutsideRoot},
		{"directory", "dir", ErrEditTarget},
		{"missing", "missing", ErrEditTarget},
		{"file parent", "dir/file.txt/child", ErrEditTarget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (EditPolicy{}).Check(root, filepath.FromSlash(tc.rel))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Check(%q) = %v, want %v", tc.rel, err, tc.want)
			}
		})
	}
	if err := (EditPolicy{}).Check(filepath.Join(root, "missing"), "file"); !errors.Is(err, ErrEditTarget) {
		t.Fatalf("missing project: %v", err)
	}
	if err := (EditPolicy{}).Check(filepath.Join(root, "dir", "file.txt"), "file"); !errors.Is(err, ErrEditTarget) {
		t.Fatalf("file project: %v", err)
	}
}

func TestEditPolicyRejectsParentComponentsBeforeCleaning(t *testing.T) {
	root := t.TempDir()
	editPathFile(t, filepath.Join(root, "file"))
	editPathFile(t, filepath.Join(root, "regular"))
	editPathFile(t, filepath.Join(root, "store", "file"))
	outside := t.TempDir()
	editPathSymlink(t, outside, filepath.Join(root, "link"))

	for _, component := range []string{"link", "missing", "regular"} {
		t.Run(component, func(t *testing.T) {
			// Do not use filepath.Join here: the raw '..' must reach Check.
			rel := filepath.FromSlash(component + "/../file")
			if err := (EditPolicy{}).Check(root, rel); !errors.Is(err, ErrOutsideRoot) {
				t.Fatalf("relative parent component: %v", err)
			}
			project := root + string(filepath.Separator) + filepath.FromSlash(component+"/..")
			if err := (EditPolicy{}).Check(project, "file"); !errors.Is(err, ErrEditTarget) {
				t.Fatalf("project parent component: %v", err)
			}
			for _, store := range []string{"store", "missing-store/nested"} {
				protected := project + string(filepath.Separator) + filepath.FromSlash(store)
				policy := EditPolicy{ProtectedRoots: []string{protected}}
				if err := policy.Check(root, "file"); !errors.Is(err, ErrEditProtected) {
					t.Fatalf("policy parent component (%s): %v", store, err)
				}
			}
		})
	}

	// Single dots remain supported in project and policy paths as well as rel.
	dottedRoot := root + string(filepath.Separator) + "."
	policy := EditPolicy{ProtectedRoots: []string{dottedRoot + string(filepath.Separator) + "store"}}
	if err := policy.Check(dottedRoot, "./file"); err != nil {
		t.Fatalf("single-dot paths: %v", err)
	}
	if err := policy.Check(dottedRoot, "./store/file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("single-dot policy protection: %v", err)
	}
}

func TestEditPolicyProtectedNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		".git", ".claude", ".codex", ".ssh", ".aws", ".gnupg", ".azure",
		"auth.json", ".credentials", ".credentials-backup", "opencode.db", "opencode.db-wal",
		"opencode.db-shm", "state_5.sqlite", "state_5.sqlite-wal", "state_5.sqlite-shm",
	} {
		for _, variant := range []string{name, strings.ToUpper(name)} {
			t.Run(variant, func(t *testing.T) {
				// Protected names apply to every component, not only the leaf.
				for _, rel := range []string{variant, filepath.Join(variant, "child"), filepath.Join("nested", variant)} {
					if err := (EditPolicy{}).Check(root, rel); !errors.Is(err, ErrEditProtected) {
						t.Fatalf("Check(%q) = %v", rel, err)
					}
				}
			})
		}
	}
	for _, name := range []string{"git.txt", "auth.json.txt", "credentials", "state_notes.txt", "opencode.txt"} {
		editPathFile(t, filepath.Join(root, name))
		if err := (EditPolicy{}).Check(root, name); err != nil {
			t.Fatalf("innocent name %q: %v", name, err)
		}
	}
	protectedProject := filepath.Join(root, ".codex", "project")
	editPathFile(t, filepath.Join(protectedProject, "file"))
	if err := (EditPolicy{}).Check(protectedProject, "file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("project nested under protected component: %v", err)
	}
}

func TestEditPolicyLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	editPathFile(t, filepath.Join(root, "dir", "file"))
	editPathFile(t, filepath.Join(outside, "file"))
	for _, tc := range []struct{ name, target, rel string }{
		{"file", filepath.Join(root, "dir", "file"), "file-link"},
		{"directory", filepath.Join(root, "dir"), "dir-link/file"},
		{"outside", outside, "outside-link/file"},
		{"dangling", filepath.Join(root, "absent"), "dangling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := strings.Split(tc.rel, "/")[0]
			editPathSymlink(t, tc.target, filepath.Join(root, first))
			if err := (EditPolicy{}).Check(root, filepath.FromSlash(tc.rel)); !errors.Is(err, ErrEditLink) {
				t.Fatalf("Check = %v", err)
			}
		})
	}
	alias := filepath.Join(t.TempDir(), "project")
	editPathSymlink(t, root, alias)
	if err := (EditPolicy{}).Check(alias, "dir/file"); err != nil {
		t.Fatalf("symlink project root: %v", err)
	}
}

func TestEditPolicyConfiguredStores(t *testing.T) {
	broad := t.TempDir()
	store := filepath.Join(broad, "custom-store")
	editPathFile(t, filepath.Join(store, "project", "file"))
	editPathFile(t, filepath.Join(broad, "custom-store-other", "file"))
	policy := EditPolicy{ProtectedRoots: []string{store}}
	for _, tc := range []struct{ root, rel string }{
		{broad, "custom-store/project/file"},
		{store, "project/file"},
		{filepath.Join(store, "project"), "file"},
	} {
		if err := policy.Check(tc.root, filepath.FromSlash(tc.rel)); !errors.Is(err, ErrEditProtected) {
			t.Fatalf("nested store %v: %v", tc, err)
		}
	}
	if err := policy.Check(broad, "custom-store-other/file"); err != nil {
		t.Fatalf("store prefix sibling: %v", err)
	}
	alias := filepath.Join(broad, "alias")
	editPathSymlink(t, store, alias)
	if err := policy.Check(alias, "project/file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("canonical project protection: %v", err)
	}
	aliasPolicy := EditPolicy{ProtectedRoots: []string{alias}}
	if err := aliasPolicy.Check(store, "project/file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("canonical policy protection: %v", err)
	}
	// Preserve the lexical policy form, even if its symlink points elsewhere.
	if err := aliasPolicy.Check(broad, "alias/project/file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("lexical policy protection: %v", err)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		casePolicy := EditPolicy{ProtectedRoots: []string{filepath.Join(broad, "CUSTOM-STORE")}}
		if err := casePolicy.Check(store, "project/file"); !errors.Is(err, ErrEditProtected) {
			t.Fatalf("case-folded store protection: %v", err)
		}
	}
}

func TestEditPolicyMissingAndInvalidRoots(t *testing.T) {
	root := t.TempDir()
	editPathFile(t, filepath.Join(root, "file"))
	missing := filepath.Join(root, "future", "nested")
	policy := EditPolicy{ProtectedRoots: []string{missing}}
	if err := policy.Check(root, "file"); err != nil {
		t.Fatalf("valid missing policy root: %v", err)
	}
	if err := policy.Check(root, "future/nested/file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("missing store must still be protected: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	editPathSymlink(t, root, alias)
	policy.ProtectedRoots = []string{filepath.Join(alias, "future", "nested")}
	if err := policy.Check(root, "future/nested/file"); !errors.Is(err, ErrEditProtected) {
		t.Fatalf("missing store resolves existing alias ancestor: %v", err)
	}
	dangling := filepath.Join(root, "dangling")
	editPathSymlink(t, filepath.Join(root, "absent"), dangling)
	loop := filepath.Join(root, "loop")
	editPathSymlink(t, loop, loop)
	for _, invalid := range []string{"", "relative/store", dangling, filepath.Join(dangling, "child"), loop, filepath.Join(root, "file", "child")} {
		t.Run(invalid, func(t *testing.T) {
			policy := EditPolicy{ProtectedRoots: []string{invalid}}
			if err := policy.Check(root, "file"); !errors.Is(err, ErrEditProtected) {
				t.Fatalf("invalid policy %q did not fail closed: %v", invalid, err)
			}
		})
	}
}
