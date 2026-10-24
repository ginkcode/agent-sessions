package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// ignoreTree is an isolated repository (see listingTree) with only directories
// and the given ignore files; every path is slash-separated and relative.
func ignoreTree(t testing.TB, rules map[string]string, dirs ...string) string {
	t.Helper()
	root := listingTree(t, 0)
	for path, body := range rules {
		path = filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func checkCount(calls *int) context.Context {
	return context.WithValue(context.Background(), gitInvocationKey{}, func(name string) {
		if name == "check-ignore" {
			*calls++
		}
	})
}

func assertListed(t *testing.T, got []string, listed, hidden []string) {
	t.Helper()
	for _, p := range listed {
		if !slices.Contains(got, p) {
			t.Errorf("missing %q", p)
		}
	}
	for _, p := range hidden {
		if slices.Contains(got, p) {
			t.Errorf("listed ignored %q", p)
		}
	}
}

// Before anchoring, ":!a" aborted check-ignore (the whole listing truncated) and
// ":ign-x" was checked as "ign-x".
func TestGitDirectoriesPathspecLikeNames(t *testing.T) {
	names := []string{":!a", ":(exclude)a", ":(top)x", ":/a", ":^b", ":colon", "*star", "[ab]", "?q", " lead", "trail ", "-dash", "#hash", "!bang", "ünïcödé"}
	ignoredNames := []string{":ign-x", "*ign", "ign?"}
	if runtime.GOOS == "windows" {
		names = []string{" lead", "-dash", "#hash", "!bang", "[ab]", "ünïcödé"}
		ignoredNames = nil
	} else {
		names = append(names, "new\nline", `back\slash`, `"quote`)
	}
	rules := ":ign-x/\n\\*ign/\nign\\?/\nplain/\n"
	// Magic applies only at the start of a path: test top-level and nested names.
	var dirs, listed, hidden []string
	for _, parent := range []string{"", "top/"} {
		for _, name := range names {
			dirs = append(dirs, parent+name+"/child")
			listed = append(listed, parent+name, parent+name+"/child")
		}
		for _, name := range append(ignoredNames, "plain") {
			dirs = append(dirs, parent+name+"/child")
			hidden = append(hidden, parent+name, parent+name+"/child")
		}
	}
	root := ignoreTree(t, map[string]string{".gitignore": rules}, dirs...)
	got, truncated, err := gitDirectories(context.Background(), root)
	if err != nil || truncated {
		t.Fatalf("truncated=%v err=%v: %q", truncated, err, got)
	}
	assertListed(t, got, listed, hidden)
}

// Nested, negated, anchored and parent rules; ignored parents are never entered,
// so a re-include beneath them (which Git also cannot honor) stays hidden.
func TestGitDirectoriesNestedIgnoreRules(t *testing.T) {
	rules := map[string]string{
		".gitignore":          "/rootonly/\nvendor/\ncache*/\n!cache-keep/\n",
		"app/.gitignore":      "build/\n!vendor/\n",
		"vendor/.gitignore":   "!inner/\n",
		"app/deep/.gitignore": "*\n!*/\n",
	}
	listed := []string{"app", "app/rootonly", "app/rootonly/x", "cache-keep", "cache-keep/x", "app/vendor", "app/vendor/x", "app/deep", "app/deep/kept", "empty", "app/empty"}
	hidden := []string{"rootonly", "rootonly/x", "vendor", "vendor/inner", "cache-a", "cache-a/x", "app/build", "app/build/x", "app/cache-b"}
	root := ignoreTree(t, rules, append(append(slices.Clone(listed), hidden...), "app/build/x/y")...)
	var calls, refCalls int
	got, truncated, err := gitDirectories(checkCount(&calls), root)
	if err != nil || truncated {
		t.Fatalf("truncated=%v err=%v", truncated, err)
	}
	assertListed(t, got, listed, append(hidden, "app/build/x/y"))
	want, _, _ := referenceDirectories(checkCount(&refCalls), root, ignoredDirectories)
	if !slices.Equal(got, want) || calls > refCalls {
		t.Fatalf("got %q (%d checks); reference %q (%d checks)", got, calls, want, refCalls)
	}
}

// A wide tree needs one decision per level instead of one per 128-entry chunk.
func TestGitDirectoriesWideTreeBatches(t *testing.T) {
	var dirs []string
	for i := range 1000 {
		dirs = append(dirs, fmt.Sprintf("w%04d/c", i))
		if i%100 == 0 {
			dirs = append(dirs, fmt.Sprintf("w%04d/node_modules/pkg", i))
		}
	}
	root := ignoreTree(t, map[string]string{".gitignore": "node_modules/\n"}, dirs...)
	var calls, pf13Calls, refCalls int
	got, truncated, err := gitDirectories(checkCount(&calls), root)
	if err != nil || truncated || len(got) < 2000 || slices.ContainsFunc(got, func(p string) bool { return filepath.Base(p) == "node_modules" }) {
		t.Fatalf("%d dirs truncated=%v err=%v", len(got), truncated, err)
	}
	pf13, _, _ := walkDirectories(checkCount(&pf13Calls), root, ignoredDirectories, frontierLimit{128, 64 << 10})
	want, _, _ := referenceDirectories(checkCount(&refCalls), root, ignoredDirectories)
	if !slices.Equal(got, want) || !slices.Equal(pf13, want) {
		t.Fatal("frontier output differs from the reference")
	}
	t.Logf("check-ignore processes: %d (PF-13 frontier %d, per-chunk reference %d)", calls, pf13Calls, refCalls)
	if calls > 3 || calls >= pf13Calls || calls >= refCalls {
		t.Fatalf("check-ignore processes: %d (PF-13 frontier %d, reference %d)", calls, pf13Calls, refCalls)
	}
}

func TestGitDirectoriesCapWithLargeFrontier(t *testing.T) {
	var dirs []string
	for i := range MaxFiles + 1 {
		dirs = append(dirs, fmt.Sprintf("d%05d", i))
	}
	root := ignoreTree(t, nil, dirs...)
	var calls int
	got, truncated, err := gitDirectories(checkCount(&calls), root)
	if err != nil || !truncated || len(got) != MaxFiles {
		t.Fatalf("%d dirs truncated=%v err=%v", len(got), truncated, err)
	}
	if want := (MaxFiles + directoryFrontier.paths) / directoryFrontier.paths; calls != want {
		t.Fatalf("check-ignore processes: %d, want %d", calls, want)
	}
}

func TestGitDirectoriesFailClosedAndCancel(t *testing.T) {
	root := ignoreTree(t, nil, "a/b", "c")
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("not a ref\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ignoredDirectories(context.Background(), root, []string{"a"}); ok {
		t.Fatal("check-ignore succeeded in a broken repository")
	}
	got, truncated, err := gitDirectories(context.Background(), root)
	if err != nil || !truncated || len(got) != 0 {
		t.Fatalf("broken repository: %q truncated=%v err=%v", got, truncated, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := ignoredDirectories(ctx, ignoreTree(t, nil), []string{"a"}); ok {
		t.Fatal("canceled check-ignore reported an answer")
	}
	if got, _, err := gitDirectories(ctx, root); err != context.Canceled || got != nil {
		t.Fatalf("canceled walk: %q err=%v", got, err)
	}
}

// BenchmarkWideDirectories compares decision strategies on a 1000-wide tree.
func BenchmarkWideDirectories(b *testing.B) {
	var dirs []string
	for i := range 1000 {
		dirs = append(dirs, fmt.Sprintf("w%04d/c", i))
	}
	root := ignoreTree(b, map[string]string{".gitignore": "node_modules/\n"}, dirs...)
	for _, bc := range []struct {
		name string
		walk func(context.Context) ([]string, bool, error)
	}{
		{"PerChunk", func(ctx context.Context) ([]string, bool, error) {
			return referenceDirectories(ctx, root, ignoredDirectories)
		}},
		{"Frontier128", func(ctx context.Context) ([]string, bool, error) {
			return walkDirectories(ctx, root, ignoredDirectories, frontierLimit{128, 64 << 10})
		}},
		{"Frontier", func(ctx context.Context) ([]string, bool, error) { return gitDirectories(ctx, root) }},
	} {
		b.Run(bc.name, func(b *testing.B) {
			calls := 0
			ctx := checkCount(&calls)
			b.ReportAllocs()
			for b.Loop() {
				if _, truncated, err := bc.walk(ctx); err != nil || truncated {
					b.Fatal(truncated, err)
				}
			}
			b.ReportMetric(float64(calls)/float64(b.N), "git/op")
		})
	}
}
