package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Search bounds: the default and largest number of matches one search
// returns, and the most kept for a single file.
const (
	DefaultSearchMatches = 5000
	MaxSearchMatches     = 20000
	MaxFileMatches       = 1000
	// maxPreviewRunes is the longest line preview; a longer line is cut to a
	// window around the match.
	maxPreviewRunes = 240
	// previewLead is how much of the line before the match a cut preview keeps.
	previewLead = 60
)

// ErrInvalidPattern reports a regular expression that does not compile; the
// message is RE2's.
var ErrInvalidPattern = errors.New("invalid search pattern")

// SearchQuery is what to look for in a project's files.
type SearchQuery struct {
	Pattern   string `json:"pattern"`
	Regex     bool   `json:"regex,omitempty"`
	MatchCase bool   `json:"matchCase,omitempty"`
	WholeWord bool   `json:"wholeWord,omitempty"`
	// Include and Exclude are comma-separated globs (see matchGlobs). A file
	// must match one Include glob, when there are any, and no Exclude glob.
	Include string `json:"include,omitempty"`
	Exclude string `json:"exclude,omitempty"`
	// MaxMatches caps the result; 0 means DefaultSearchMatches.
	MaxMatches int `json:"maxMatches,omitempty"`
}

// SearchMatch is one match on one line. Columns count UTF-16 code units, as
// the editor and language servers do.
type SearchMatch struct {
	Line  int `json:"line"` // 1-based
	Start int `json:"start"`
	End   int `json:"end"`
	// Preview is the line, cut to a window around the match when long and
	// without its leading white space; PreviewStart and PreviewEnd locate
	// the match in it, in UTF-16 code units.
	Preview      string `json:"preview"`
	PreviewStart int    `json:"previewStart"`
	PreviewEnd   int    `json:"previewEnd"`
}

// SearchFile is the matches in one file.
type SearchFile struct {
	Path string `json:"path"`
	// Revision identifies the bytes searched (see revisionOf), so a replace
	// can tell whether the file changed since.
	Revision  string        `json:"revision"`
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated,omitempty"` // more than MaxFileMatches
}

// SearchResult is the files with matches, sorted by path.
type SearchResult struct {
	Files   []SearchFile `json:"files"`
	Matches int          `json:"matches"`
	// Searched is the number of text files looked through.
	Searched int `json:"searched"`
	// Truncated is set when a cap cut the result short: the match limit, or
	// the file limit of a listing made without git.
	Truncated bool `json:"truncated,omitempty"`
}

// CompileQuery returns the regular expression that finds q's matches in one
// line.
func CompileQuery(q SearchQuery) (*regexp.Regexp, error) {
	return compile(queryExpr(q))
}

// queryExpr returns the regular expression source for q.
func queryExpr(q SearchQuery) string {
	expr := q.Pattern
	if !q.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if q.WholeWord {
		expr = `\b(?:` + expr + `)\b`
	}
	if !q.MatchCase {
		expr = "(?i)" + expr
	}
	return expr
}

func compile(expr string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidPattern, strings.TrimPrefix(err.Error(), "error parsing regexp: "))
	}
	return re, nil
}

// Search looks for q in the text files under root. The files are those List
// shows: inside a git work tree the tracked and untracked ones that are not
// ignored, elsewhere the walk that skips dependency and build directories.
// Symbolic links, binary files and files larger than MaxReadBytes are left
// out. Matches are found line by line, so a pattern cannot span lines.
func Search(ctx context.Context, root string, q SearchQuery) (SearchResult, error) {
	out := SearchResult{Files: []SearchFile{}}
	if q.Pattern == "" {
		return out, nil
	}
	m, err := newMatcher(q)
	if err != nil {
		return SearchResult{}, err
	}
	include, err := parseGlobs(q.Include)
	if err != nil {
		return SearchResult{}, err
	}
	exclude, err := parseGlobs(q.Exclude)
	if err != nil {
		return SearchResult{}, err
	}
	limit := q.MaxMatches
	if limit <= 0 {
		limit = DefaultSearchMatches
	}
	limit = min(limit, MaxSearchMatches)

	info, err := os.Stat(root)
	if err != nil {
		return SearchResult{}, err
	}
	if !info.IsDir() {
		return SearchResult{}, fmt.Errorf("%s is not a directory", root)
	}
	paths, ok := gitFiles(ctx, root)
	if !ok {
		listing, err := walk(ctx, root)
		if err != nil {
			return SearchResult{}, err
		}
		paths, out.Truncated = listing.Files, listing.Truncated
	}
	var selected []string
	for _, p := range paths {
		if (len(include) == 0 || matchGlobs(include, p)) && !matchGlobs(exclude, p) {
			selected = append(selected, p)
		}
	}

	results := make([]*SearchFile, len(selected))
	var total atomic.Int64
	var searched atomic.Int64
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), 8) {
		wg.Go(func() {
			for i := range next {
				f, ok := searchFile(root, selected[i], m)
				if !ok {
					continue
				}
				searched.Add(1)
				if len(f.Matches) > 0 {
					results[i] = &f
					total.Add(int64(len(f.Matches)))
				}
			}
		})
	}
	// Files are handed out in path order and handing out stops once enough
	// matches are found, so the files searched are always a prefix of the
	// sorted list and the first limit matches are the same on every run.
	var cancelErr error
	for i := range selected {
		if err := ctx.Err(); err != nil {
			cancelErr = err
			break
		}
		if total.Load() >= int64(limit) {
			out.Truncated = true
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if cancelErr != nil {
		return SearchResult{}, cancelErr
	}

	for _, f := range results {
		if f == nil {
			continue
		}
		if room := limit - out.Matches; len(f.Matches) > room {
			if room <= 0 {
				out.Truncated = true
				break
			}
			f.Matches, f.Truncated, out.Truncated = f.Matches[:room], true, true
		}
		out.Files = append(out.Files, *f)
		out.Matches += len(f.Matches)
	}
	out.Searched = int(searched.Load())
	return out, nil
}

// matcher finds a query's matches line by line. whole, when set, is the same
// expression in multi-line mode: run once over a whole file, it finds a
// match whenever some line has one, so files without any are passed over
// without splitting them into lines.
type matcher struct {
	line, whole *regexp.Regexp
}

func newMatcher(q SearchQuery) (matcher, error) {
	expr := queryExpr(q)
	line, err := compile(expr)
	if err != nil {
		return matcher{}, err
	}
	m := matcher{line: line}
	// \A and \z mean the start and end of each line here, but of the file
	// in multi-line mode, so the quick check could miss them.
	if !strings.Contains(expr, `\A`) && !strings.Contains(expr, `\z`) {
		m.whole, err = compile("(?m)" + expr)
		if err != nil {
			return matcher{}, err
		}
	}
	return m, nil
}

// mayMatch reports whether text can have a match; false is certain.
func (m matcher) mayMatch(text string) bool {
	if m.whole == nil {
		return true
	}
	// $ comes before the CR of a CRLF line, as in the line-by-line search.
	if strings.Contains(text, "\r\n") {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	return m.whole.MatchString(text)
}

// searchFile returns the matches of m in the file rel names under root. ok
// is false when the file was not searched: not a regular file, unreadable,
// too large or not text.
func searchFile(root, rel string, m matcher) (SearchFile, bool) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxReadBytes {
		return SearchFile{}, false
	}
	f, err := os.Open(full)
	if err != nil {
		return SearchFile{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil || len(data) > MaxReadBytes || IsBinary(data) {
		return SearchFile{}, false
	}
	out := SearchFile{Path: rel, Revision: revisionOf(data), Matches: []SearchMatch{}}
	text := string(data)
	if !m.mayMatch(text) {
		return out, true
	}
	line := 0
	for len(text) > 0 && !out.Truncated {
		line++
		s, rest, _ := strings.Cut(text, "\n")
		text = rest
		s = strings.TrimSuffix(s, "\r")
		for _, at := range m.line.FindAllStringIndex(s, -1) {
			if at[0] == at[1] {
				continue // an empty match marks nothing
			}
			if len(out.Matches) == MaxFileMatches {
				out.Truncated = true
				break
			}
			out.Matches = append(out.Matches, newMatch(line, s, at[0], at[1]))
		}
	}
	return out, true
}

// newMatch builds the match of s[start:end] on the given line.
func newMatch(line int, s string, start, end int) SearchMatch {
	m := SearchMatch{Line: line, Start: utf16Len(s[:start])}
	m.End = m.Start + utf16Len(s[start:end])
	from := len(s) - len(strings.TrimLeftFunc(s, unicode.IsSpace))
	from = min(from, start)
	to := len(s)
	if utf8.RuneCountInString(s[from:]) > maxPreviewRunes {
		// Keep a little of what comes before the match, then fill the
		// window with what follows.
		if lead := s[from:start]; utf8.RuneCountInString(lead) > previewLead {
			from = start - len(lastRunes(lead, previewLead))
		}
		to = from + len(firstRunes(s[from:], maxPreviewRunes))
		to = max(to, end)
	}
	m.Preview = s[from:to]
	m.PreviewStart = utf16Len(s[from:start])
	m.PreviewEnd = m.PreviewStart + utf16Len(s[start:end])
	return m
}

// utf16Len returns the length of s in UTF-16 code units.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// firstRunes returns the first n runes of s.
func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// lastRunes returns the last n runes of s.
func lastRunes(s string, n int) string {
	i := len(s)
	for i > 0 && n > 0 {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
		n--
	}
	return s[i:]
}

// glob is one include or exclude pattern. A pattern without a slash, such
// as *.go or node_modules, matches any path component; one with a slash is
// anchored at the project root and also matches everything under the folder
// it names.
type glob struct {
	re       *regexp.Regexp
	anywhere bool
}

// parseGlobs reads a comma-separated list of globs. Commas inside braces
// belong to the brace.
func parseGlobs(list string) ([]glob, error) {
	var out []glob
	depth, start := 0, 0
	add := func(p string) error {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			return nil
		}
		anywhere := !strings.Contains(p, "/")
		p = strings.TrimPrefix(p, "/")
		re, err := regexp.Compile("^" + globRegexp(p) + "$")
		if err != nil {
			return fmt.Errorf("%w: glob %q", ErrInvalidPattern, p)
		}
		out = append(out, glob{re: re, anywhere: anywhere})
		return nil
	}
	for i, c := range list {
		switch c {
		case '{':
			depth++
		case '}':
			depth = max(0, depth-1)
		case ',':
			if depth == 0 {
				if err := add(list[start:i]); err != nil {
					return nil, err
				}
				start = i + 1
			}
		}
	}
	if err := add(list[start:]); err != nil {
		return nil, err
	}
	return out, nil
}

// globRegexp translates a glob into a regular expression: * and ? stay in
// one path component, ** crosses them, {a,b} is either, and [...] is a
// character class.
func globRegexp(p string) string {
	var b strings.Builder
	braces := 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '*' && i+1 < len(p) && p[i+1] == '*':
			i++
			if i+1 < len(p) && p[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
			} else {
				b.WriteString(`.*`)
			}
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		case c == '{':
			braces++
			b.WriteString(`(?:`)
		case c == '}' && braces > 0:
			braces--
			b.WriteString(`)`)
		case c == ',' && braces > 0:
			b.WriteString(`|`)
		case c == '[':
			end := strings.IndexByte(p[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := p[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteByte('[')
			b.WriteString(strings.ReplaceAll(class, `\`, `\\`))
			b.WriteByte(']')
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	for range braces {
		b.WriteString(`)`)
	}
	return b.String()
}

// matchGlobs reports whether path, slash-separated and relative to the
// project root, matches one of globs.
func matchGlobs(globs []glob, path string) bool {
	for _, g := range globs {
		if g.anywhere {
			for part := range strings.SplitSeq(path, "/") {
				if g.re.MatchString(part) {
					return true
				}
			}
			continue
		}
		// The path itself, or a folder above it.
		for p := path; ; {
			if g.re.MatchString(p) {
				return true
			}
			i := strings.LastIndexByte(p, '/')
			if i < 0 {
				break
			}
			p = p[:i]
		}
	}
	return false
}
