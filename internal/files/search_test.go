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

// searchPaths returns the paths of the files in r, in order.
func searchPaths(r SearchResult) []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	return out
}

func TestSearchOptions(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\n\nfunc Hello() {}\nfunc hello2() {}\n")
	write(t, root, "b.txt", "say hello\r\nHELLO world\r\n")
	write(t, root, "c.md", "nothing here\n")

	cases := []struct {
		name  string
		q     SearchQuery
		files []string
		count int
	}{
		{"ignore case", SearchQuery{Pattern: "hello"}, []string{"a.go", "b.txt"}, 4},
		{"match case", SearchQuery{Pattern: "hello", MatchCase: true}, []string{"a.go", "b.txt"}, 2},
		{"whole word", SearchQuery{Pattern: "hello", WholeWord: true}, []string{"a.go", "b.txt"}, 3},
		{"regex", SearchQuery{Pattern: `func \w+\(`, Regex: true}, []string{"a.go"}, 2},
		{"literal metacharacters", SearchQuery{Pattern: "() {}"}, []string{"a.go"}, 2},
		{"line start anchor", SearchQuery{Pattern: `\Asay`, Regex: true}, []string{"b.txt"}, 1},
		{"end of line before CR", SearchQuery{Pattern: `world$`, Regex: true}, []string{"b.txt"}, 1},
		{"include", SearchQuery{Pattern: "hello", Include: "*.txt"}, []string{"b.txt"}, 2},
		{"exclude", SearchQuery{Pattern: "hello", Exclude: "*.txt, *.md"}, []string{"a.go"}, 2},
		{"no match", SearchQuery{Pattern: "absent"}, nil, 0},
		{"empty pattern", SearchQuery{}, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Search(context.Background(), root, c.q)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(searchPaths(got), c.files) || got.Matches != c.count {
				t.Errorf("files %v, %d matches; want %v, %d", searchPaths(got), got.Matches, c.files, c.count)
			}
		})
	}
}

func TestSearchMatchPositions(t *testing.T) {
	root := t.TempDir()
	// "é" is one UTF-16 unit, "😀" two; the editor counts columns that way.
	write(t, root, "u.txt", "first\n\t  é😀 needle and needle\n")
	got, err := Search(context.Background(), root, SearchQuery{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || len(got.Files[0].Matches) != 2 {
		t.Fatalf("result = %+v", got)
	}
	m := got.Files[0].Matches[0]
	if m.Line != 2 || m.Start != 7 || m.End != 13 {
		t.Errorf("match = line %d %d-%d, want line 2 7-13", m.Line, m.Start, m.End)
	}
	if m.Preview != "é😀 needle and needle" || m.PreviewStart != 4 || m.PreviewEnd != 10 {
		t.Errorf("preview = %q %d-%d", m.Preview, m.PreviewStart, m.PreviewEnd)
	}
	if got.Files[0].Revision != revisionOf([]byte("first\n\t  é😀 needle and needle\n")) {
		t.Error("revision does not identify the bytes searched")
	}
}

func TestSearchLongLinePreview(t *testing.T) {
	root := t.TempDir()
	line := strings.Repeat("a", 500) + "needle" + strings.Repeat("b", 500)
	write(t, root, "long.txt", line+"\n")
	got, err := Search(context.Background(), root, SearchQuery{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	m := got.Files[0].Matches[0]
	if len([]rune(m.Preview)) != maxPreviewRunes || m.PreviewStart != previewLead {
		t.Errorf("preview has %d runes, match at %d", len([]rune(m.Preview)), m.PreviewStart)
	}
	if m.Preview[m.PreviewStart:m.PreviewEnd] != "needle" || m.Start != 500 {
		t.Errorf("preview match = %q, start %d", m.Preview[m.PreviewStart:m.PreviewEnd], m.Start)
	}
}

func TestSearchSkipsFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "text.txt", "needle\n")
	write(t, root, "bin.dat", "needle\x00\n")
	write(t, root, "latin1.txt", "needle \xe9\n")
	write(t, root, "node_modules/x/index.js", "needle\n")
	big := strings.Repeat("needle\n", MaxReadBytes/7+1)
	write(t, root, "big.txt", big)
	if err := os.Symlink(filepath.Join(root, "text.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	got, err := Search(context.Background(), root, SearchQuery{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"text.txt"}; !slices.Equal(searchPaths(got), want) {
		t.Errorf("files = %v, want %v", searchPaths(got), want)
	}
}

func TestSearchUsesGitIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	write(t, root, ".gitignore", "out/\n*.log\n")
	write(t, root, "a.go", "needle\n")
	write(t, root, "out/gen.go", "needle\n")
	write(t, root, "run.log", "needle\n")
	// vendor is skipped only by the walk; in git it is a normal folder.
	write(t, root, "vendor/v.go", "needle\n")
	got, err := Search(context.Background(), root, SearchQuery{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.go", "vendor/v.go"}; !slices.Equal(searchPaths(got), want) {
		t.Errorf("files = %v, want %v", searchPaths(got), want)
	}
}

func TestSearchTruncates(t *testing.T) {
	root := t.TempDir()
	for i := range 30 {
		write(t, root, fmt.Sprintf("f%02d.txt", i), "x x x\n")
	}
	write(t, root, "many.txt", strings.Repeat("x\n", MaxFileMatches+5))
	for range 3 { // the same answer on every run, whatever the scheduling
		got, err := Search(context.Background(), root, SearchQuery{Pattern: "x", MaxMatches: 10})
		if err != nil {
			t.Fatal(err)
		}
		if got.Matches != 10 || !got.Truncated || !slices.Equal(searchPaths(got), []string{"f00.txt", "f01.txt", "f02.txt", "f03.txt"}) {
			t.Fatalf("matches %d truncated %v files %v", got.Matches, got.Truncated, searchPaths(got))
		}
		if last := got.Files[3]; len(last.Matches) != 1 || !last.Truncated {
			t.Errorf("last file kept %d matches", len(last.Matches))
		}
	}
	got, err := Search(context.Background(), root, SearchQuery{Pattern: "x", Include: "many.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Matches != MaxFileMatches || !got.Files[0].Truncated {
		t.Errorf("per-file cap: %d matches, truncated %v", got.Matches, got.Files[0].Truncated)
	}
}

func TestSearchCanceled(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "needle\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Search(ctx, root, SearchQuery{Pattern: "needle"}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want canceled", err)
	}
}

func TestSearchInvalidPattern(t *testing.T) {
	root := t.TempDir()
	_, err := Search(context.Background(), root, SearchQuery{Pattern: "(", Regex: true})
	if !errors.Is(err, ErrInvalidPattern) || !strings.Contains(err.Error(), "missing closing )") {
		t.Errorf("err = %v", err)
	}
}

func TestMatchGlobs(t *testing.T) {
	cases := []struct {
		globs string
		path  string
		want  bool
	}{
		{"*.go", "a.go", true},
		{"*.go", "internal/files/a.go", true},
		{"*.go", "a.gox", false},
		{"node_modules", "web/node_modules/x/index.js", true},
		{"src", "lib/src2/x.ts", false},
		{"src/lib", "src/lib/a/b.ts", true},
		{"src/lib", "x/src/lib/a.ts", false},
		{"**/test/*.ts", "a/b/test/x.ts", true},
		{"**/test/*.ts", "test/x.ts", true},
		{"*.{ts,svelte}", "c/App.svelte", true},
		{"*.{ts,svelte}, *.md", "README.md", true},
		{"file?.txt", "file1.txt", true},
		{"[ab].txt", "b.txt", true},
		{"[!ab].txt", "b.txt", false},
		{"./docs/", "docs/plan/x.md", true},
	}
	for _, c := range cases {
		globs, err := parseGlobs(c.globs)
		if err != nil {
			t.Fatalf("%q: %v", c.globs, err)
		}
		if got := matchGlobs(globs, c.path); got != c.want {
			t.Errorf("matchGlobs(%q, %q) = %v, want %v", c.globs, c.path, got, c.want)
		}
	}
}

func BenchmarkSearch(b *testing.B) {
	root := b.TempDir()
	body := strings.Repeat("func handler(w http.ResponseWriter, r *http.Request) { serve(w, r) }\n", 60)
	for i := range 5000 {
		p := filepath.Join(root, fmt.Sprintf("d%02d", i%50), fmt.Sprintf("f%04d.go", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := Search(context.Background(), root, SearchQuery{Pattern: "needle"}); err != nil {
			b.Fatal(err)
		}
	}
}
