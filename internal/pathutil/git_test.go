package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitResolver(t *testing.T) {
	root := t.TempDir()

	// 1. Plain repo
	plainRepo := filepath.Join(root, "plain")
	if err := os.MkdirAll(filepath.Join(plainRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 2. Nested subdir in plain repo
	nestedSubdir := filepath.Join(plainRepo, "a", "b", "c")
	if err := os.MkdirAll(nestedSubdir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 3. Linked worktree (hand-made .git file + commondir)
	mainRepo := filepath.Join(root, "main")
	mainGitWorktree := filepath.Join(mainRepo, ".git", "worktrees", "wt1")
	if err := os.MkdirAll(mainGitWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainGitWorktree, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktreeAbs := filepath.Join(root, "worktree-abs")
	if err := os.MkdirAll(worktreeAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeAbs, ".git"), []byte("gitdir: "+mainGitWorktree+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. Relative gitdir in linked worktree
	worktreeRel := filepath.Join(root, "worktree-rel")
	if err := os.MkdirAll(worktreeRel, 0o755); err != nil {
		t.Fatal(err)
	}
	relGitdir, err := filepath.Rel(worktreeRel, mainGitWorktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRel, ".git"), []byte("gitdir: "+relGitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 5. Symlinked cwd
	symlinkCwd := filepath.Join(root, "symlink-cwd")
	if err := os.Symlink(nestedSubdir, symlinkCwd); err != nil {
		t.Fatal(err)
	}

	// 6. Deleted cwd inside a repo
	deletedCwd := filepath.Join(plainRepo, "to-be-deleted", "sub")
	if err := os.MkdirAll(deletedCwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(plainRepo, "to-be-deleted")); err != nil {
		t.Fatal(err)
	}

	// 7. Non-repo
	nonRepo := filepath.Join(root, "non-repo", "some", "dir")
	if err := os.MkdirAll(nonRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	// Submodule case (no commondir)
	submoduleRepo := filepath.Join(root, "submodule")
	submoduleGitdir := filepath.Join(mainRepo, ".git", "modules", "submodule")
	if err := os.MkdirAll(submoduleRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(submoduleGitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(submoduleRepo, ".git"), []byte("gitdir: "+submoduleGitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		dir      string
		wantInfo RepoInfo
		wantOK   bool
	}{
		{
			name:     "plain repo",
			dir:      plainRepo,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(plainRepo), MainRoot: NormalizeDir(plainRepo)},
			wantOK:   true,
		},
		{
			name:     "nested subdir",
			dir:      nestedSubdir,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(plainRepo), MainRoot: NormalizeDir(plainRepo)},
			wantOK:   true,
		},
		{
			name:     "linked worktree absolute gitdir",
			dir:      worktreeAbs,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(worktreeAbs), MainRoot: NormalizeDir(mainRepo)},
			wantOK:   true,
		},
		{
			name:     "linked worktree relative gitdir",
			dir:      worktreeRel,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(worktreeRel), MainRoot: NormalizeDir(mainRepo)},
			wantOK:   true,
		},
		{
			name:     "symlinked cwd",
			dir:      symlinkCwd,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(plainRepo), MainRoot: NormalizeDir(plainRepo)},
			wantOK:   true,
		},
		{
			name:     "deleted cwd inside repo",
			dir:      deletedCwd,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(plainRepo), MainRoot: NormalizeDir(plainRepo)},
			wantOK:   true,
		},
		{
			name:     "submodule without commondir",
			dir:      submoduleRepo,
			wantInfo: RepoInfo{Toplevel: NormalizeDir(submoduleRepo), MainRoot: NormalizeDir(submoduleRepo)},
			wantOK:   true,
		},
		{
			name:     "non-repo",
			dir:      nonRepo,
			wantInfo: RepoInfo{},
			wantOK:   false,
		},
		{
			name:     "empty directory",
			dir:      "",
			wantInfo: RepoInfo{},
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewGitResolver()
			gotInfo, gotOK := resolver.Resolve(tt.dir)
			if gotOK != tt.wantOK {
				t.Fatalf("Resolve(%q) ok = %v, want %v", tt.dir, gotOK, tt.wantOK)
			}
			if gotInfo != tt.wantInfo {
				t.Errorf("Resolve(%q) = %+v, want %+v", tt.dir, gotInfo, tt.wantInfo)
			}
		})
	}
}

func TestGitResolverCacheHitCount(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	sub1 := filepath.Join(repo, "sub1")
	sub2 := filepath.Join(repo, "sub2")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub2, 0o755); err != nil {
		t.Fatal(err)
	}

	resolver := NewGitResolver()
	if hits := resolver.Hits(); hits != 0 {
		t.Fatalf("expected 0 initial hits, got %d", hits)
	}

	// First resolve: cache miss on sub1 and repo
	info1, ok1 := resolver.Resolve(sub1)
	if !ok1 || info1.Toplevel != NormalizeDir(repo) {
		t.Fatalf("failed to resolve sub1: %+v, %v", info1, ok1)
	}
	if hits := resolver.Hits(); hits != 0 {
		t.Fatalf("expected 0 hits after first resolve, got %d", hits)
	}

	// Second resolve of exact same dir: should hit sub1 directly
	info2, ok2 := resolver.Resolve(sub1)
	if !ok2 || info2 != info1 {
		t.Fatalf("failed to resolve sub1 second time: %+v, %v", info2, ok2)
	}
	if hits := resolver.Hits(); hits != 1 {
		t.Fatalf("expected 1 hit, got %d", hits)
	}

	// Third resolve of sibling sub2: misses sub2, but walks to repo which is cached!
	info3, ok3 := resolver.Resolve(sub2)
	if !ok3 || info3 != info1 {
		t.Fatalf("failed to resolve sub2: %+v, %v", info3, ok3)
	}
	if hits := resolver.Hits(); hits != 2 {
		t.Fatalf("expected 2 hits, got %d", hits)
	}

	// Fourth resolve of sub2: now sub2 itself is cached
	info4, ok4 := resolver.Resolve(sub2)
	if !ok4 || info4 != info1 {
		t.Fatalf("failed to resolve sub2 fourth time: %+v, %v", info4, ok4)
	}
	if hits := resolver.Hits(); hits != 3 {
		t.Fatalf("expected 3 hits, got %d", hits)
	}

	if clen := resolver.CacheLen(); clen < 3 {
		t.Errorf("expected at least 3 cached entries, got %d", clen)
	}
}
