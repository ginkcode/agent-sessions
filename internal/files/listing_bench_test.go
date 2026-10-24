package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// listingTree never discovers repositories: both the worktree and Git configuration
// live under TempDir. Git environment overrides cannot redirect the index/worktree.
func listingTree(t testing.TB, count int) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") {
			t.Setenv(key, "")
		}
	}
	config := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(config, "absent"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ATTR_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("HOME", config)
	t.Setenv("XDG_CONFIG_HOME", config)
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	put := func(path, body string) {
		t.Helper()
		path = filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "--template=")
	put(".gitignore", "ignored/\n*.skip\n")
	put("nested/.gitignore", "hidden/\n*.tmp\n!keep.tmp\n")
	put("nested/keep.tmp", "needle\n")
	put("nested/drop.tmp", "excluded needle\n")
	put("nested/hidden/secret", "excluded needle\n")
	put("ignored/deep/secret", "excluded needle\n")
	put("file.skip", "excluded needle\n")
	put("deleted.txt", "deleted needle\n")
	for i := range count {
		put(fmt.Sprintf("src%02d/f%05d.txt", i%16, i), "synthetic\n")
	}
	git("add", ".") // Index entries suffice to model tracked/deleted files; no commit.
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	put("untracked.txt", "needle\n")
	for _, path := range []string{"empty", "nested/empty"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSyntheticListingBaseline(t *testing.T) {
	for _, count := range []int{128, MaxFiles + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := listingTree(t, count)
			calls := map[string]int{}
			ctx := context.WithValue(context.Background(), gitInvocationKey{}, func(command string) { calls[command]++ })
			got, err := List(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			want := min(count+4, MaxFiles) // two ignore files, keep.tmp, untracked.txt
			if !got.Git || got.Root != root || len(got.Files) != want || got.Truncated != (count+4 > MaxFiles) {
				t.Fatalf("listing: Git=%v files=%d truncated=%v root=%q", got.Git, len(got.Files), got.Truncated, got.Root)
			}
			if calls["ls-files"] != 2 || calls["rev-parse"] != 1 || calls["status"] != 1 || calls["check-ignore"] != 2 {
				t.Fatalf("Git work = %v", calls)
			}
			t.Logf("Git invocations: %v", calls)
			if got.Status["deleted.txt"] != Deleted || got.Status["untracked.txt"] != Untracked {
				t.Fatalf("deleted/untracked statuses missing")
			}
			for _, path := range []string{"deleted.txt", "nested/drop.tmp", "file.skip", "ignored/deep/secret", "nested/hidden/secret"} {
				if slices.Contains(got.Files, path) {
					t.Errorf("unexpected file %s", path)
				}
			}
			for _, path := range []string{"empty", "nested", "nested/empty"} {
				if !slices.Contains(got.Directories, path) {
					t.Errorf("missing directory %s", path)
				}
			}
			for _, path := range got.Directories {
				if strings.HasPrefix(path, "ignored") || strings.HasPrefix(path, "nested/hidden") || strings.HasPrefix(path, ".git") {
					t.Errorf("unexpected directory %s", path)
				}
			}
			if len(got.Files)+len(got.Ignored) > MaxFiles || !slices.IsSorted(got.Files) || !slices.IsSorted(got.Directories) {
				t.Fatal("cap/order violated")
			}
			if count < MaxFiles && (!slices.Contains(got.Files, "nested/keep.tmp") || !slices.Contains(got.Ignored, "file.skip") || !slices.Contains(got.Ignored, "nested/drop.tmp")) {
				t.Fatal("nested ignore/negation baseline missing")
			}
			before := calls["ls-files"]
			sub, err := List(context.Background(), filepath.Join(root, "nested"))
			if err != nil || !slices.Equal(sub.Files, []string{".gitignore", "keep.tmp"}) || calls["ls-files"] != before {
				t.Fatalf("subroot/context isolation: %+v, %v", sub, err)
			}
			result, err := Search(context.Background(), root, SearchQuery{Pattern: "needle"})
			// Baseline, not a target: Git search filters ignored paths but bypasses List's
			// MaxFiles cap, so untracked.txt is found after the truncated listing boundary.
			if err != nil || result.Truncated || len(result.Files) != 2 {
				t.Fatalf("search: files=%d truncated=%v err=%v", len(result.Files), result.Truncated, err)
			}
		})
	}
}

func TestSyntheticListingSymlinkIsolation(t *testing.T) {
	root := listingTree(t, 1)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("outside needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"external": outside, "internal": filepath.Join(root, "nested"), "outside.txt": filepath.Join(outside, "sentinel")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	got, err := List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"external", "internal", "outside.txt"} {
		if !slices.Contains(got.Files, path) || slices.Contains(got.Directories, path) {
			t.Fatalf("symlink must be a leaf: %s", path)
		}
	}
	if _, err := Resolve(root, "outside.txt"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Resolve outside link: %v", err)
	}
	if _, err := Resolve(root, "external/sentinel"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Resolve outside directory: %v", err)
	}
	result, err := Search(context.Background(), root, SearchQuery{Pattern: "outside needle"})
	if err != nil || len(result.Files) != 0 {
		t.Fatalf("search followed outside symlink: %+v, %v", result, err)
	}
}

func TestSyntheticGitDirectoryCap(t *testing.T) {
	root := listingTree(t, 0)
	for i := range MaxFiles + 1 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%05d", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	dirs, truncated, err := gitDirectories(context.Background(), root)
	if err != nil || !truncated || len(dirs) != MaxFiles {
		t.Fatalf("directory cap: %d truncated=%v err=%v", len(dirs), truncated, err)
	}
}

func BenchmarkProjectListing(b *testing.B) {
	for _, count := range []int{1000, MaxFiles + 1} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := listingTree(b, count)
			for _, component := range []string{"List", "GitFiles", "Status", "Ignored", "Directories", "Walk", "Resolve"} {
				b.Run(component, func(b *testing.B) {
					calls := 0
					ctx := context.WithValue(context.Background(), gitInvocationKey{}, func(string) { calls++ })
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						switch component {
						case "List":
							if _, err := List(ctx, root); err != nil {
								b.Fatal(err)
							}
						case "GitFiles":
							if _, ok := gitFiles(ctx, root); !ok {
								b.Fatal("gitFiles failed")
							}
						case "Status":
							if gitStatus(ctx, root) == nil {
								b.Fatal("gitStatus failed")
							}
						case "Ignored":
							gitIgnored(ctx, root, MaxFiles)
						case "Directories":
							if _, _, err := gitDirectories(ctx, root); err != nil {
								b.Fatal(err)
							}
						case "Walk":
							if _, err := walk(ctx, root); err != nil {
								b.Fatal(err)
							}
						case "Resolve":
							if _, err := Resolve(root, "nested/keep.tmp"); err != nil {
								b.Fatal(err)
							}
						}
					}
					b.ReportMetric(float64(calls)/float64(b.N), "git/op")
				})
			}
		})
	}
}
