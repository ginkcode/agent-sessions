package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func readBody(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSaveKeepsFormat(t *testing.T) {
	for _, tc := range []struct {
		name, body, text, want string
	}{
		{"lf", "a\nb\n", "a\nc\n", "a\nc\n"},
		{"crlf", "a\r\nb\r\n", "a\nc\nd", "a\r\nc\r\nd"},
		{"bom crlf", "\xef\xbb\xbfa\r\n", "x\ny\n", "\xef\xbb\xbfx\r\ny\r\n"},
		{"none", "one", "two\nlines", "two\nlines"},
		{"shorter", strings.Repeat("long line\n", 50), "short\n", "short\n"},
		{"empty", "", "new", "new"},
		{"to empty", "gone\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "src/file.txt", tc.body)
			res, err := Save(context.Background(), EditPolicy{}, root, SaveRequest{
				Path: "src/file.txt", Text: tc.text, ExpectedRevision: sha(tc.body),
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != SaveStatusSaved || res.Content == nil {
				t.Fatalf("result = %+v", res)
			}
			if got := readBody(t, filepath.Join(root, "src", "file.txt")); got != tc.want {
				t.Fatalf("file = %q, want %q", got, tc.want)
			}
			c := res.Content
			if c.Path != "src/file.txt" || c.Text != tc.want || c.Revision != sha(tc.want) || c.Size != int64(len(tc.want)) {
				t.Fatalf("content = %+v", c)
			}
			if c.Edit == nil || !c.Edit.Eligible || c.Edit.Reason != "" {
				t.Fatalf("edit = %+v", c.Edit)
			}
		})
	}
}

func TestSaveConflictWritesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "theirs\n")
	res, err := Save(context.Background(), EditPolicy{}, root, SaveRequest{
		Path: "f.txt", Text: "mine\n", ExpectedRevision: sha("original\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != SaveStatusConflict || res.Content != nil || res.ObservedRevision != sha("theirs\n") {
		t.Fatalf("result = %+v", res)
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != "theirs\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestSaveRejectsBadRevisions(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "x")
	for _, rev := range []string{"", "sha256:", "sha256:" + strings.Repeat("A", 64), "md5:" + strings.Repeat("a", 64), sha("x") + " "} {
		_, err := Save(context.Background(), EditPolicy{}, root, SaveRequest{Path: "f.txt", Text: "y", ExpectedRevision: rev})
		if !errors.Is(err, ErrInvalidRevision) {
			t.Errorf("revision %q: err = %v", rev, err)
		}
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != "x" {
		t.Fatalf("file = %q", got)
	}
}

func TestSaveRefusals(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".git/config", "[core]\n")
	write(t, root, "mixed.txt", "a\nb\r\n")
	write(t, root, "big.txt", "x")
	write(t, root, "store/s.json", "{}")
	for _, tc := range []struct {
		name, path, body, text string
		policy                 EditPolicy
		want                   error
	}{
		{"protected name", ".git/config", "[core]\n", "", EditPolicy{}, ErrEditProtected},
		{"protected root", "store/s.json", "{}", "", EditPolicy{ProtectedRoots: []string{filepath.Join(root, "store")}}, ErrEditProtected},
		{"mixed newlines", "mixed.txt", "a\nb\r\n", "a\n", EditPolicy{}, ErrUnsupportedEditText},
		{"carriage return", "big.txt", "x", "a\rb", EditPolicy{}, ErrUnsupportedEditText},
		{"too large", "big.txt", "x", strings.Repeat("y", MaxEditBytes+1), EditPolicy{}, ErrEditTooLarge},
		{"outside", "../f.txt", "x", "y", EditPolicy{}, nil},
		{"missing", "nope.txt", "", "y", EditPolicy{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Save(context.Background(), tc.policy, root, SaveRequest{Path: tc.path, Text: tc.text, ExpectedRevision: sha(tc.body)})
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if got := readBody(t, filepath.Join(root, "big.txt")); got != "x" {
		t.Fatalf("file = %q", got)
	}
}

func TestSaveRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	write(t, root, "real.txt", "x")
	editPathSymlink(t, filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt"))
	_, err := Save(context.Background(), EditPolicy{}, root, SaveRequest{Path: "link.txt", Text: "y", ExpectedRevision: sha("x")})
	if !errors.Is(err, ErrEditLink) {
		t.Fatalf("err = %v", err)
	}
}

// In-place writes keep the file's identity: a hard link sees the change and
// the permission bits are unchanged.
func TestSaveKeepsIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not Unix modes")
	}
	root := t.TempDir()
	write(t, root, "f.sh", "echo a\n")
	path := filepath.Join(root, "f.sh")
	if err := os.Chmod(path, 0o741); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if _, err := Save(context.Background(), EditPolicy{}, root, SaveRequest{Path: "f.sh", Text: "echo b\n", ExpectedRevision: sha("echo a\n")}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || after.Mode().Perm() != 0o741 {
		t.Fatalf("identity or mode changed: %v", after.Mode())
	}
	if got := readBody(t, link); got != "echo b\n" {
		t.Fatalf("hard link = %q", got)
	}
}

// Of two saves from one revision, exactly one wins; the other conflicts.
func TestSaveConcurrentOneWins(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "base\n")
	var wg sync.WaitGroup
	results := make([]SaveResult, 8)
	errs := make([]error, len(results))
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = Save(context.Background(), EditPolicy{}, root, SaveRequest{
				Path: "f.txt", Text: strings.Repeat("v", i+1) + "\n", ExpectedRevision: sha("base\n"),
			})
		}()
	}
	wg.Wait()
	saved := 0
	var winner string
	for i, r := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if r.Status == SaveStatusSaved {
			saved++
			winner = r.Content.Text
		}
	}
	if saved != 1 {
		t.Fatalf("saved %d times", saved)
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != winner {
		t.Fatalf("file = %q, winner = %q", got, winner)
	}
}

// Saves through different names of one file (hard links, an aliased root)
// are serialized too: of saves from one revision exactly one wins.
func TestSaveConcurrentAliasesOneWins(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "base\n")
	if err := os.Link(filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	editPathSymlink(t, root, alias)
	targets := []struct{ root, path string }{{root, "a.txt"}, {root, "b.txt"}, {alias, "a.txt"}, {alias, "b.txt"}}
	for round := 0; round < 20; round++ {
		base := readBody(t, filepath.Join(root, "a.txt"))
		var wg sync.WaitGroup
		results := make([]SaveResult, 8)
		errs := make([]error, len(results))
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tg := targets[i%len(targets)]
				results[i], errs[i] = Save(context.Background(), EditPolicy{}, tg.root, SaveRequest{
					Path: tg.path, Text: strings.Repeat("v", round*len(results)+i+1) + "\n", ExpectedRevision: sha(base),
				})
			}()
		}
		wg.Wait()
		saved := 0
		var winner string
		for i, r := range results {
			if errs[i] != nil {
				t.Fatalf("round %d: %v", round, errs[i])
			}
			switch r.Status {
			case SaveStatusSaved:
				saved++
				winner = r.Content.Text
			case SaveStatusConflict:
				if r.ObservedRevision == sha(base) {
					t.Fatalf("round %d: conflict at the expected revision", round)
				}
			}
		}
		if saved != 1 {
			t.Fatalf("round %d: saved %d times", round, saved)
		}
		for _, name := range []string{"a.txt", "b.txt"} {
			if got := readBody(t, filepath.Join(root, name)); got != winner {
				t.Fatalf("round %d: %s = %q, winner = %q", round, name, got, winner)
			}
		}
	}
}

// A snapshot equal to the file is saved without writing it.
func TestSaveUnchangedWritesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "a\r\nb\r\n")
	path := filepath.Join(root, "f.txt")
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	res, err := Save(context.Background(), EditPolicy{}, root, SaveRequest{Path: "f.txt", Text: "a\nb\n", ExpectedRevision: sha("a\r\nb\r\n")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != SaveStatusSaved || res.Content == nil || res.Content.Revision != sha("a\r\nb\r\n") {
		t.Fatalf("result = %+v", res)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("file was written: %v %v", info.ModTime(), err)
	}
}

// A save waiting for another one gives up when its context ends.
func TestSaveCancelledWhileWaiting(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "x")
	unlock, err := lockSaves(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := Save(ctx, EditPolicy{}, root, SaveRequest{Path: "f.txt", Text: "y", ExpectedRevision: sha("x")}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != "x" {
		t.Fatalf("file = %q", got)
	}
}

// Just before writing, the open file must still be what was read, under the
// name and root it was opened by.
func TestEditTargetRevalidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, root string)
	}{
		{"unchanged", func(*testing.T, string) {}},
		{"rewritten", func(t *testing.T, root string) { write(t, root, "dir/f.txt", "longer text\n") }},
		{"replaced", func(t *testing.T, root string) {
			p := filepath.Join(root, "dir", "f.txt")
			if err := os.Rename(p, p+".old"); err != nil {
				t.Fatal(err)
			}
			write(t, root, "dir/f.txt", "text\n")
		}},
		{"parent moved", func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "moved")); err != nil {
				t.Skipf("cannot rename open directory: %v", err)
			}
			write(t, root, "dir/f.txt", "text\n")
		}},
		{"protected", func(t *testing.T, root string) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "dir/f.txt", "text\n")
			target, err := openEditTargetFlag(context.Background(), EditPolicy{}, root, "dir/f.txt", os.O_RDWR)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			tc.change(t, root)
			policy := EditPolicy{}
			if tc.name == "protected" {
				policy.ProtectedRoots = []string{filepath.Join(root, "dir")}
			}
			err = target.revalidate(policy, root, "dir/f.txt")
			if (err == nil) != (tc.name == "unchanged") {
				t.Fatalf("revalidate = %v", err)
			}
		})
	}
}

// A root alias retargeted after opening no longer names the opened project.
func TestEditTargetRevalidateRootMoved(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	write(t, root, "f.txt", "text\n")
	write(t, other, "f.txt", "text\n")
	alias := filepath.Join(t.TempDir(), "alias")
	editPathSymlink(t, root, alias)
	target, err := openEditTargetFlag(context.Background(), EditPolicy{}, alias, "f.txt", os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	editPathSymlink(t, other, alias)
	if err := target.revalidate(EditPolicy{}, alias, "f.txt"); !errors.Is(err, ErrEditTarget) {
		t.Fatalf("revalidate = %v", err)
	}
}

func TestSaveCancelled(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Save(ctx, EditPolicy{}, root, SaveRequest{Path: "f.txt", Text: "y", ExpectedRevision: sha("x")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != "x" {
		t.Fatalf("file = %q", got)
	}
}

func TestCheckEdit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "ok.txt", "a\n")
	write(t, root, ".ssh/config", "Host x\n")
	write(t, root, "ro.txt", "a\n")
	write(t, root, "mixed.txt", "a\r\nb\n")
	ro := filepath.Join(root, "ro.txt")
	if err := os.Chmod(ro, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o644) })
	editPathSymlink(t, filepath.Join(root, "ok.txt"), filepath.Join(root, "link.txt"))

	for _, tc := range []struct {
		path, reason string
		eligible     bool
	}{
		{"ok.txt", "", true},
		{".ssh/config", EditProtected, false},
		{"link.txt", EditLinked, false},
		{"ro.txt", EditReadOnly, false},
		{"mixed.txt", EditUnsupportedNewlines, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if tc.path == "ro.txt" && runtime.GOOS != "windows" && os.Geteuid() == 0 {
				t.Skip("root can write read-only files")
			}
			c, err := Read(context.Background(), root, tc.path)
			if err != nil {
				t.Fatal(err)
			}
			CheckEdit(EditPolicy{}, root, tc.path, c.Edit)
			if c.Edit == nil || c.Edit.Eligible != tc.eligible || c.Edit.Reason != tc.reason {
				t.Fatalf("edit = %+v", c.Edit)
			}
		})
	}
}

// A failure once writing began cannot say what the file holds, so it must be
// distinguishable from a refusal that left the file alone.
func TestOverwriteFailureIsIncomplete(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "old\n")
	f, err := os.Open(filepath.Join(root, "f.txt")) // read-only: writing fails
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	target := &editTarget{file: f}
	if err := target.overwrite([]byte("new\n")); !errors.Is(err, ErrSaveIncomplete) {
		t.Fatalf("overwrite err = %v, want ErrSaveIncomplete", err)
	}
}

func TestSaveRefusalsAreNotIncomplete(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "old\n")
	for _, req := range []SaveRequest{
		{Path: "f.txt", Text: "x", ExpectedRevision: "sha256:nope"},
		{Path: "missing.txt", Text: "x", ExpectedRevision: sha("old\n")},
		{Path: "f.txt", Text: "bad\r", ExpectedRevision: sha("old\n")},
	} {
		if _, err := Save(context.Background(), EditPolicy{}, root, req); err == nil || errors.Is(err, ErrSaveIncomplete) {
			t.Errorf("Save(%q) err = %v, want a refusal that is not ErrSaveIncomplete", req.Path, err)
		}
	}
}
