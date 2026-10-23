package files

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func replaceOne(t *testing.T, root string, q SearchQuery, replacement string, f ReplaceFile) ReplaceFileResult {
	t.Helper()
	res, err := Replace(context.Background(), EditPolicy{}, root, ReplaceRequest{Query: q, Replacement: replacement, Files: []ReplaceFile{f}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("files = %+v", res.Files)
	}
	return res.Files[0]
}

func TestReplaceKeepsFormat(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		q          SearchQuery
		with, want string
		n          int
	}{
		{"lf", "foo bar\nfoo\n", SearchQuery{Pattern: "foo"}, "baz", "baz bar\nbaz\n", 2},
		{"crlf", "foo\r\nx foo\r\n", SearchQuery{Pattern: "foo"}, "qux", "qux\r\nx qux\r\n", 2},
		{"bom crlf", "\xef\xbb\xbffoo\r\n", SearchQuery{Pattern: "foo"}, "b", "\xef\xbb\xbfb\r\n", 1},
		{"no final newline", "a.b", SearchQuery{Pattern: "."}, "-", "a-b", 1},
		{"case-insensitive", "Foo FOO foo", SearchQuery{Pattern: "foo"}, "x", "x x x", 3},
		{"whole word", "cat concat cat", SearchQuery{Pattern: "cat", WholeWord: true}, "dog", "dog concat dog", 2},
		// $ is before the CR of a CRLF line, as in Search.
		{"line end in crlf", "a\r\nb\r\n", SearchQuery{Pattern: `$`, Regex: true}, ";", "a\r\nb\r\n", 0},
		{"anchored in crlf", "ab\r\ncb\r\n", SearchQuery{Pattern: `b$`, Regex: true}, "B", "aB\r\ncB\r\n", 2},
		{"groups", "f(a, b)\n", SearchQuery{Pattern: `(\w+), (\w+)`, Regex: true}, "$2, $1", "f(b, a)\n", 1},
		{"named groups and dollars", "x=1\n", SearchQuery{Pattern: `(?P<k>\w)=(?P<v>\d)`, Regex: true}, "${v}$$${k}", "1$x\n", 1},
		{"literal dollars", "x\n", SearchQuery{Pattern: "x"}, "$1", "$1\n", 1},
		{"new lines in crlf", "a,b\r\n", SearchQuery{Pattern: ","}, "\n", "a\r\nb\r\n", 1},
		{"emoji columns", "😀 foo foo\n", SearchQuery{Pattern: "foo"}, "", "😀  \n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "src/f.txt", tc.body)
			res := replaceOne(t, root, tc.q, tc.with, ReplaceFile{Path: "src/f.txt", ExpectedRevision: sha(tc.body)})
			if tc.n == 0 {
				if res.Status != ReplaceStatusSkipped || res.Reason != ReplaceStaleMatch {
					t.Fatalf("result = %+v", res)
				}
			} else if res.Status != ReplaceStatusReplaced || res.Replaced != tc.n || res.Revision != sha(tc.want) {
				t.Fatalf("result = %+v, want %d replaced", res, tc.n)
			}
			if got := readBody(t, filepath.Join(root, "src", "f.txt")); got != tc.want {
				t.Fatalf("file = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReplaceSelectedMatches(t *testing.T) {
	root := t.TempDir()
	body := "😀foo foo\nfoo\n"
	write(t, root, "f.txt", body)
	// The columns Search reports: UTF-16, so the emoji counts two.
	got, err := Search(context.Background(), root, SearchQuery{Pattern: "foo"})
	if err != nil || len(got.Files) != 1 || len(got.Files[0].Matches) != 3 {
		t.Fatalf("search = %+v, %v", got, err)
	}
	m := got.Files[0].Matches
	if m[1].Start != 6 {
		t.Fatalf("second match starts at %d", m[1].Start)
	}
	res := replaceOne(t, root, SearchQuery{Pattern: "foo"}, "X", ReplaceFile{
		Path: "f.txt", ExpectedRevision: got.Files[0].Revision,
		Matches: []ReplaceMatch{{Line: m[1].Line, Start: m[1].Start}, {Line: m[2].Line, Start: m[2].Start}},
	})
	if res.Status != ReplaceStatusReplaced || res.Replaced != 2 {
		t.Fatalf("result = %+v", res)
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != "😀foo X\nX\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestReplaceStaleSelectionWritesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "f.txt", "foo foo\n")
	res := replaceOne(t, root, SearchQuery{Pattern: "foo"}, "X", ReplaceFile{
		Path: "f.txt", ExpectedRevision: sha("foo foo\n"),
		Matches: []ReplaceMatch{{Line: 1, Start: 0}, {Line: 1, Start: 2}},
	})
	if res.Status != ReplaceStatusSkipped || res.Reason != ReplaceStaleMatch {
		t.Fatalf("result = %+v", res)
	}
	if got := readBody(t, filepath.Join(root, "f.txt")); got != "foo foo\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestReplaceOutcomesPerFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "foo\n")
	write(t, root, "changed.txt", "foo theirs\n")
	write(t, root, ".git/config", "foo\n")
	write(t, root, "store/s.json", "foo")
	write(t, root, "mixed.txt", "foo\nfoo\r\n")
	write(t, root, "bin.dat", "foo\x00")
	write(t, root, "big.txt", "foo"+strings.Repeat("x", MaxEditBytes-10))
	write(t, root, "z.txt", "foo foo\n")
	policy := EditPolicy{ProtectedRoots: []string{filepath.Join(root, "store")}}
	req := ReplaceRequest{Query: SearchQuery{Pattern: "foo"}, Replacement: "foofoofoofoofoofoo", Files: []ReplaceFile{
		{Path: "a.txt", ExpectedRevision: sha("foo\n")},
		{Path: "changed.txt", ExpectedRevision: sha("foo\n")},
		{Path: ".git/config", ExpectedRevision: sha("foo\n")},
		{Path: "store/s.json", ExpectedRevision: sha("foo")},
		{Path: "mixed.txt", ExpectedRevision: sha("foo\nfoo\r\n")},
		{Path: "bin.dat", ExpectedRevision: sha("foo\x00")},
		{Path: "big.txt", ExpectedRevision: sha("foo" + strings.Repeat("x", MaxEditBytes-10))},
		{Path: "missing.txt", ExpectedRevision: sha("foo")},
		{Path: "../outside.txt", ExpectedRevision: sha("foo")},
		{Path: "bad-revision.txt", ExpectedRevision: "md5:x"},
		{Path: "z.txt", ExpectedRevision: sha("foo foo\n")},
		{Path: "z.txt", ExpectedRevision: sha("foo foo\n")},
	}}
	res, err := Replace(context.Background(), policy, root, req)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		status ReplaceStatus
		reason string
	}{
		{ReplaceStatusReplaced, ""},
		{ReplaceStatusConflict, ""},
		{ReplaceStatusSkipped, EditProtected},
		{ReplaceStatusSkipped, EditProtected},
		{ReplaceStatusSkipped, ReplaceUnsupportedText},
		{ReplaceStatusSkipped, ReplaceUnsupportedText},
		{ReplaceStatusSkipped, EditTooLarge}, // grows past MaxEditBytes
		{ReplaceStatusFailed, ""},
		{ReplaceStatusFailed, ""}, // outside the root: not a path Search reports
		{ReplaceStatusFailed, ""},
		{ReplaceStatusReplaced, ""}, // one failure does not stop the rest
		{ReplaceStatusSkipped, ReplaceDuplicate},
	}
	if len(res.Files) != len(want) {
		t.Fatalf("files = %+v", res.Files)
	}
	for i, w := range want {
		got := res.Files[i]
		if got.Path != req.Files[i].Path || got.Status != w.status || got.Reason != w.reason {
			t.Errorf("%s: %+v, want %s %s", req.Files[i].Path, got, w.status, w.reason)
		}
		if got.Status == ReplaceStatusFailed && got.Message == "" {
			t.Errorf("%s: failure without a message", got.Path)
		}
	}
	if c := res.Files[1]; c.ObservedRevision != sha("foo theirs\n") {
		t.Errorf("conflict observed %q", c.ObservedRevision)
	}
	if res.Replaced != 3 {
		t.Errorf("replaced = %d, want 3", res.Replaced)
	}
	for path, body := range map[string]string{
		"changed.txt": "foo theirs\n", ".git/config": "foo\n", "store/s.json": "foo", "mixed.txt": "foo\nfoo\r\n",
		"big.txt": "foo" + strings.Repeat("x", MaxEditBytes-10),
	} {
		if got := readBody(t, filepath.Join(root, filepath.FromSlash(path))); got != body {
			t.Errorf("%s changed: %q", path, got)
		}
	}
}

func TestReplaceRefusesLinksAndBadRequests(t *testing.T) {
	root := t.TempDir()
	write(t, root, "real.txt", "foo")
	editPathSymlink(t, filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt"))
	res := replaceOne(t, root, SearchQuery{Pattern: "foo"}, "x", ReplaceFile{Path: "link.txt", ExpectedRevision: sha("foo")})
	if res.Status != ReplaceStatusSkipped || res.Reason != EditLinked {
		t.Fatalf("link: %+v", res)
	}
	for _, tc := range []struct {
		q    SearchQuery
		with string
		want error
	}{
		{SearchQuery{}, "x", ErrInvalidPattern},
		{SearchQuery{Pattern: "(", Regex: true}, "x", ErrInvalidPattern},
		{SearchQuery{Pattern: "foo"}, "a\rb", ErrInvalidReplacement},
		{SearchQuery{Pattern: "foo"}, "a\x00", ErrInvalidReplacement},
		{SearchQuery{Pattern: "foo"}, "\xff", ErrInvalidReplacement},
	} {
		_, err := Replace(context.Background(), EditPolicy{}, root, ReplaceRequest{Query: tc.q, Replacement: tc.with, Files: []ReplaceFile{{Path: "real.txt", ExpectedRevision: sha("foo")}}})
		if !errors.Is(err, tc.want) {
			t.Errorf("%+v %q: err = %v, want %v", tc.q, tc.with, err, tc.want)
		}
	}
	if got := readBody(t, filepath.Join(root, "real.txt")); got != "foo" {
		t.Fatalf("file = %q", got)
	}
}

func TestReplaceCancelled(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "foo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Replace(ctx, EditPolicy{}, root, ReplaceRequest{Query: SearchQuery{Pattern: "foo"}, Replacement: "x", Files: []ReplaceFile{{Path: "a.txt", ExpectedRevision: sha("foo")}}})
	if err != nil {
		t.Fatal(err)
	}
	if f := res.Files[0]; f.Status != ReplaceStatusFailed || !strings.Contains(f.Message, "canceled") {
		t.Fatalf("result = %+v", f)
	}
	if got := readBody(t, filepath.Join(root, "a.txt")); got != "foo" {
		t.Fatalf("file = %q", got)
	}
}
