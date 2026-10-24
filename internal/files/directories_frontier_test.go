package files

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// referenceDirectories is the pre-PF-13 walker: one ignore decision per
// 128-entry ReadDir chunk. It is the oracle for untruncated frontier results.
func referenceDirectories(ctx context.Context, root string, check ignoreCheck) ([]string, bool, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	var directories []string
	pending := []string{""}
	visited := 0
	truncated := false
	for len(pending) > 0 && !truncated {
		rel := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		p := filepath.FromSlash(rel)
		if p == "" {
			p = "."
		}
		f, err := r.Open(p)
		if err != nil {
			continue
		}
		for {
			entries, readErr := f.ReadDir(128)
			var candidates []string
			for _, entry := range entries {
				visited++
				if visited > MaxFiles*4 {
					truncated = true
					break
				}
				if entry.IsDir() && entry.Name() != ".git" {
					candidates = append(candidates, filepath.ToSlash(filepath.Join(rel, entry.Name())))
				}
			}
			if len(candidates) > 0 {
				ignored, ok := check(ctx, root, candidates)
				if !ok {
					truncated = true
				} else {
					for _, candidate := range candidates {
						if ignored[candidate] {
							continue
						}
						if len(directories) >= MaxFiles {
							truncated = true
							break
						}
						directories = append(directories, candidate)
						pending = append(pending, candidate)
					}
				}
			}
			if truncated || readErr != nil {
				break
			}
		}
		f.Close()
	}
	slices.Sort(directories)
	return directories, truncated, nil
}

// frontierTree is wide, deep, has empty and ignored directories, files,
// repository metadata, unusual names and (where supported) directory symlinks.
func frontierTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkdir := func(rel string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	put := func(rel string) {
		t.Helper()
		mkdir(path.Dir(rel))
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 300 {
		mkdir(fmt.Sprintf("wide/w%03d", i))
		if i%7 == 0 {
			mkdir(fmt.Sprintf("wide/w%03d/inner/empty", i))
			put(fmt.Sprintf("wide/w%03d/inner/file.txt", i))
		}
	}
	deep := "deep"
	for i := range 40 {
		deep += fmt.Sprintf("/d%02d", i)
	}
	mkdir(deep)
	mkdir("ignored-deps/pkg/nested")
	mkdir("wide/w001/ignored-cache/x")
	mkdir(".git/objects")
	mkdir("empty")
	names := []string{"with space", "ünïcödé", "-leading-dash", "#hash", "!bang", "[brackets]"}
	if runtime.GOOS != "windows" {
		names = append(names, "*star", ":colon", "new\nline", `back\slash`, "?q")
	}
	for _, name := range names {
		mkdir("odd/" + name + "/child")
	}
	for i := range 200 {
		put(fmt.Sprintf("files/f%03d.txt", i))
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "escaped"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outside, filepath.Join(root, "link-out"))
	_ = os.Symlink(filepath.Join(root, "wide"), filepath.Join(root, "link-in"))
	return root
}

// fakeIgnore ignores every candidate whose name starts with "ignored" and
// records each batch so tests can check bounds and decision order.
type fakeIgnore struct {
	batches [][]string
	fail    int // fail the n-th call (1-based); 0 never fails
	onCall  func(n int)
}

func (f *fakeIgnore) check(_ context.Context, _ string, paths []string) (map[string]bool, bool) {
	f.batches = append(f.batches, slices.Clone(paths))
	if f.onCall != nil {
		f.onCall(len(f.batches))
	}
	if len(f.batches) == f.fail {
		return nil, false
	}
	ignored := make(map[string]bool)
	for _, p := range paths {
		if strings.HasPrefix(path.Base(p), "ignored") {
			ignored[p] = true
		}
	}
	return ignored, true
}

var frontierLimits = []frontierLimit{{1, 1}, {2, 1 << 20}, {7, 64}, directoryFrontier, {1 << 20, 1 << 30}}

func TestDirectoryFrontierMatchesReference(t *testing.T) {
	root := frontierTree(t)
	want, wantTruncated, err := referenceDirectories(context.Background(), root, (&fakeIgnore{}).check)
	if err != nil || wantTruncated {
		t.Fatalf("reference: truncated=%v err=%v", wantTruncated, err)
	}
	for _, p := range []string{"empty", "wide/w000/inner/empty", "deep/d00/d01/d02/d03/d04/d05/d06/d07/d08/d09/d10/d11/d12/d13/d14/d15/d16/d17/d18/d19/d20/d21/d22/d23/d24/d25/d26/d27/d28/d29/d30/d31/d32/d33/d34/d35/d36/d37/d38/d39", "odd/with space/child"} {
		if !slices.Contains(want, p) {
			t.Fatalf("fixture missing %q", p)
		}
	}
	for _, p := range want {
		if strings.Contains(p, "ignored") || strings.HasPrefix(p, ".git") || strings.HasPrefix(p, "link-") {
			t.Fatalf("fixture listed %q", p)
		}
	}
	for _, limit := range frontierLimits {
		t.Run(fmt.Sprintf("%d-%d", limit.paths, limit.bytes), func(t *testing.T) {
			fake := &fakeIgnore{}
			got, truncated, err := walkDirectories(context.Background(), root, fake.check, limit)
			if err != nil || truncated || !slices.Equal(got, want) {
				t.Fatalf("got %d dirs truncated=%v err=%v; want %d", len(got), truncated, err, len(want))
			}
			// Bounded batches, each decided before any member is entered.
			decided := map[string]bool{"": true}
			for _, batch := range fake.batches {
				size := 0
				for _, p := range batch {
					size += len(p) + 3
					parent := path.Dir(p)
					if parent == "." {
						parent = ""
					}
					if !decided[parent] {
						t.Fatalf("%q checked before its parent was decided", p)
					}
				}
				if len(batch) == 0 || len(batch) > limit.paths || (len(batch) > 1 && size > limit.bytes) {
					t.Fatalf("batch of %d paths/%d bytes exceeds %+v", len(batch), size, limit)
				}
				for _, p := range batch {
					decided[p] = !strings.HasPrefix(path.Base(p), "ignored")
				}
			}
		})
	}
}

func TestDirectoryFrontierFailsClosed(t *testing.T) {
	root := frontierTree(t)
	for _, limit := range frontierLimits {
		t.Run(fmt.Sprintf("%d-%d", limit.paths, limit.bytes), func(t *testing.T) {
			fake := &fakeIgnore{fail: 2}
			got, truncated, err := walkDirectories(context.Background(), root, fake.check, limit)
			if err != nil || !truncated {
				t.Fatalf("truncated=%v err=%v", truncated, err)
			}
			if len(fake.batches) != 2 {
				t.Fatalf("checked %d batches after a failure", len(fake.batches))
			}
			allowed := slices.DeleteFunc(slices.Clone(fake.batches[0]), func(p string) bool { return strings.HasPrefix(path.Base(p), "ignored") })
			slices.Sort(allowed)
			if !slices.Equal(got, allowed) {
				t.Fatalf("listed %v; want only the first decided batch %v", got, allowed)
			}
		})
	}
}

func TestDirectoryFrontierCancellation(t *testing.T) {
	root := frontierTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeIgnore{onCall: func(n int) {
		if n == 2 {
			cancel()
		}
	}}
	if got, _, err := walkDirectories(ctx, root, fake.check, frontierLimit{2, 1 << 20}); err != context.Canceled || got != nil {
		t.Fatalf("got %d dirs err=%v", len(got), err)
	}
	if len(fake.batches) > 3 {
		t.Fatalf("kept checking after cancellation: %d batches", len(fake.batches))
	}
}

// Entries read before the visit cap are listed but never entered, as before.
func TestDirectoryFrontierVisitCap(t *testing.T) {
	if testing.Short() {
		t.Skip("creates MaxFiles*4 entries")
	}
	root := t.TempDir()
	for i := range MaxFiles*4 + 10 {
		name := filepath.Join(root, fmt.Sprintf("e%05d", i))
		var err error
		if i%1000 == 0 {
			err = os.Mkdir(name, 0o700)
			if err == nil {
				err = os.Mkdir(filepath.Join(name, "child"), 0o700)
			}
		} else {
			err = os.WriteFile(name, nil, 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	want, wantTruncated, _ := referenceDirectories(context.Background(), root, (&fakeIgnore{}).check)
	got, truncated, err := walkDirectories(context.Background(), root, (&fakeIgnore{}).check, directoryFrontier)
	if err != nil || !truncated || !wantTruncated || !slices.Equal(got, want) || len(got) == 0 {
		t.Fatalf("got %v truncated=%v err=%v; want %v", got, truncated, err, want)
	}
	for _, p := range got {
		if strings.Contains(p, "/") {
			t.Fatalf("descended past the visit cap: %q", p)
		}
	}
}

// Real check-ignore over nested rules: equal output, and the fixture's cross-
// directory frontier never needs more processes than per-chunk checks did.
func TestGitDirectoryFrontierMatchesReference(t *testing.T) {
	root := listingTree(t, 300)
	for i := range 40 {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("src%02d/sub%02d/leaf", i%16, i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "nested", "hidden", "deeper"), 0o700); err != nil {
		t.Fatal(err)
	}
	count := func(calls *int) context.Context {
		return context.WithValue(context.Background(), gitInvocationKey{}, func(string) { *calls++ })
	}
	var refCalls, calls int
	want, wantTruncated, err := referenceDirectories(count(&refCalls), root, ignoredDirectories)
	if err != nil || wantTruncated {
		t.Fatalf("reference: truncated=%v err=%v", wantTruncated, err)
	}
	got, truncated, err := gitDirectories(count(&calls), root)
	if err != nil || truncated || !slices.Equal(got, want) {
		t.Fatalf("got %v truncated=%v err=%v; want %v", got, truncated, err, want)
	}
	if slices.Contains(got, "ignored") || slices.Contains(got, "nested/hidden") || !slices.Contains(got, "nested/empty") || !slices.Contains(got, "src00/sub00/leaf") {
		t.Fatalf("ignore rules not honored: %v", got)
	}
	t.Logf("check-ignore processes: frontier %d, per-chunk reference %d", calls, refCalls)
	if calls > refCalls {
		t.Fatalf("frontier used %d check-ignore processes, reference %d", calls, refCalls)
	}
}
