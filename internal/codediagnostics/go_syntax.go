package codediagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// GoSyntaxProviderID is the in-process, saved-file Go syntax provider. It runs
// no executable: go/parser parses bytes read through a contained os.Root.
const GoSyntaxProviderID = "go-syntax"

// GoSyntaxLimits bound one scan. Zero values take the defaults; larger values
// are clamped to them.
type GoSyntaxLimits struct {
	Entries    int   // directory entries visited by one full walk
	Files      int   // .go files analyzed (cached) at once
	FileBytes  int64 // largest single file parsed
	TotalBytes int64 // bytes read by one full scan
	PerFile    int   // syntax errors kept per file
}

func (l GoSyntaxLimits) defaults() GoSyntaxLimits {
	clamp := func(v, max int) int {
		if v <= 0 || v > max {
			return max
		}
		return v
	}
	clamp64 := func(v, max int64) int64 {
		if v <= 0 || v > max {
			return max
		}
		return v
	}
	l.Entries = clamp(l.Entries, 50000)
	l.Files = clamp(l.Files, 5000)
	l.FileBytes = clamp64(l.FileBytes, 1<<20)
	l.TotalBytes = clamp64(l.TotalBytes, 64<<20)
	l.PerFile = clamp(l.PerFile, 10)
	return l
}

type goSyntaxEntry struct {
	size        int64
	mod         time.Time
	diagnostics []Diagnostic
	partial     bool
}

// GoSyntax is owned by exactly one goroutine (the project watcher loop). It
// keeps per-file findings only, never file bodies.
type GoSyntax struct {
	root        AuthorizedRoot
	limits      GoSyntaxLimits
	cache       map[string]goSyntaxEntry
	walkPartial bool
	overflow    bool
	canceled    func() bool
}

func NewGoSyntax(root AuthorizedRoot, limits GoSyntaxLimits) *GoSyntax {
	return &GoSyntax{root: root, limits: limits.defaults(), cache: map[string]goSyntaxEntry{}, canceled: func() bool { return false }}
}

// skipDirectory names are never walked or watched. Go tooling ignores ".", "_"
// and testdata trees (not coverage loss). Dependency/generated trees are
// excluded too, but may hold Go files, so skipping them marks coverage partial.
func skipDirectory(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || generatedDirectory(name)
}

func generatedDirectory(name string) bool {
	return slices.Contains([]string{"node_modules", "vendor", "dist", "build", "target"}, name)
}

// skippedPath reports whether any directory component of rel is skipped.
func skippedPath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if skipDirectory(p) {
			return true
		}
	}
	return false
}

type walkDir struct {
	rel     string // "" is the root
	entries int
	depth   int
}

type walkFile struct {
	rel  string
	info fs.FileInfo
}

// walk enumerates contained directories and .go files. Symlinked directories
// are never followed; symlinked files are read only through os.Root.
func (g *GoSyntax) walk(r *os.Root) ([]walkDir, []walkFile, bool) {
	var dirs []walkDir
	var files []walkFile
	partial, entries := false, 0
	queue := []walkDir{{rel: ""}}
	for len(queue) > 0 && !g.canceled() {
		d := queue[0]
		queue = queue[1:]
		name := "."
		if d.rel != "" {
			name = filepath.FromSlash(d.rel)
		}
		f, err := r.Open(name)
		if err != nil {
			partial = true
			continue
		}
		list, err := f.ReadDir(g.limits.Entries - entries + 1)
		_ = f.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			partial = true
		}
		d.entries = len(list)
		dirs = append(dirs, d)
		slices.SortFunc(list, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, e := range list {
			entries++
			if entries > g.limits.Entries {
				return dirs, files, true
			}
			rel := e.Name()
			if d.rel != "" {
				rel = d.rel + "/" + e.Name()
			}
			if ValidatePath(rel) != nil {
				partial = true
				continue
			}
			switch {
			case strings.HasSuffix(e.Name(), ".go") && skipDirectory(e.Name()):
				continue // Go ignores "." and "_" files
			case e.Type()&os.ModeSymlink != 0:
				if strings.HasSuffix(e.Name(), ".go") {
					info, err := r.Stat(filepath.FromSlash(rel)) // os.Root refuses escapes
					if err != nil || !info.Mode().IsRegular() {
						partial = true
						continue
					}
					files = append(files, walkFile{rel, info})
				}
			case e.IsDir():
				if skipDirectory(e.Name()) {
					partial = partial || generatedDirectory(e.Name())
					continue
				}
				if d.depth >= 32 {
					partial = true
					continue
				}
				queue = append(queue, walkDir{rel: rel, depth: d.depth + 1})
			case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".go"):
				info, err := e.Info()
				if err != nil {
					continue // raced deletion
				}
				files = append(files, walkFile{rel, info})
			}
		}
	}
	return dirs, files, partial || g.canceled()
}

// FileChange is one analyzed saved-file transition. Kind is 1 created, 2
// changed, 3 deleted (the LSP FileChangeType values).
type FileChange struct {
	Path string
	Kind int
}

// Full walks and parses the whole contained project. Unchanged (size, mtime)
// files reuse cached findings; the cache is rebuilt so deletions drop out.
func (g *GoSyntax) Full() (CheckerResult, []walkDir, []FileChange, error) {
	if err := g.root.Revalidate(); err != nil {
		return CheckerResult{}, nil, nil, err
	}
	r, err := os.OpenRoot(g.root.Project().Root)
	if err != nil {
		return CheckerResult{}, nil, nil, failure("unauthorized", "Project root cannot be opened")
	}
	defer func() { _ = r.Close() }()
	dirs, files, partial := g.walk(r)
	old := g.cache
	g.cache = map[string]goSyntaxEntry{}
	g.overflow = false
	var changes []FileChange
	var total int64
	for _, f := range files {
		if g.canceled() {
			partial = true
			break
		}
		if len(g.cache) >= g.limits.Files {
			g.overflow = true
			break
		}
		prev, known := old[f.rel]
		if known && prev.size == f.info.Size() && prev.mod.Equal(f.info.ModTime()) {
			g.cache[f.rel] = prev
			continue
		}
		if total+f.info.Size() > g.limits.TotalBytes {
			g.overflow = true
			break
		}
		total += f.info.Size()
		g.cache[f.rel] = g.parse(r, f.rel)
		kind := 2
		if !known {
			kind = 1
		}
		changes = append(changes, FileChange{f.rel, kind})
	}
	for rel := range old {
		if _, ok := g.cache[rel]; !ok && !g.overflow {
			changes = append(changes, FileChange{rel, 3})
		}
	}
	g.walkPartial = partial
	slices.SortFunc(changes, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return g.result(), dirs, changes, nil
}

// Update re-reads only the given contained paths. Non-Go and skipped paths are
// ignored. A missing file drops its findings.
func (g *GoSyntax) Update(paths []string) (CheckerResult, []FileChange, error) {
	if err := g.root.Revalidate(); err != nil {
		return CheckerResult{}, nil, err
	}
	r, err := os.OpenRoot(g.root.Project().Root)
	if err != nil {
		return CheckerResult{}, nil, failure("unauthorized", "Project root cannot be opened")
	}
	defer func() { _ = r.Close() }()
	var changes []FileChange
	for _, rel := range paths {
		if !strings.HasSuffix(rel, ".go") || ValidatePath(rel) != nil || skippedPath(rel) {
			continue
		}
		_, known := g.cache[rel]
		info, err := r.Stat(filepath.FromSlash(rel))
		if err != nil || !info.Mode().IsRegular() {
			if known {
				delete(g.cache, rel)
				changes = append(changes, FileChange{rel, 3})
			}
			continue
		}
		if !known && len(g.cache) >= g.limits.Files {
			g.overflow = true
			continue
		}
		kind := 2
		if !known {
			kind = 1
		}
		g.cache[rel] = g.parse(r, rel)
		changes = append(changes, FileChange{rel, kind})
	}
	return g.result(), changes, nil
}

// parse reads one bounded, contained file and converts syntax errors to
// UTF-16 ranges bound to the exact bytes' revision.
func (g *GoSyntax) parse(r *os.Root, rel string) goSyntaxEntry {
	f, err := r.Open(filepath.FromSlash(rel))
	if err != nil {
		return goSyntaxEntry{partial: true}
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return goSyntaxEntry{partial: true}
	}
	entry := goSyntaxEntry{size: st.Size(), mod: st.ModTime()}
	if st.Size() > g.limits.FileBytes {
		entry.partial = true
		return entry
	}
	src, err := io.ReadAll(io.LimitReader(f, g.limits.FileBytes+1))
	if err != nil || int64(len(src)) > g.limits.FileBytes {
		entry.partial = true
		return entry
	}
	sum := sha256.Sum256(src)
	revision := "sha256:" + hex.EncodeToString(sum[:])
	_, err = parser.ParseFile(token.NewFileSet(), rel, src, parser.AllErrors|parser.SkipObjectResolution)
	var list scanner.ErrorList
	if err == nil || !errors.As(err, &list) {
		entry.diagnostics = []Diagnostic{}
		return entry
	}
	text := string(src)
	// Editors display text without a BOM; first-line columns would differ, so
	// such findings stay panel-only rather than claiming verified coordinates.
	freshness := "current"
	if strings.HasPrefix(text, "\uFEFF") {
		freshness = "uncertain"
	}
	for _, e := range list {
		if len(entry.diagnostics) >= g.limits.PerFile {
			entry.partial = true
			break
		}
		off := min(max(e.Pos.Offset, 0), len(text))
		for off > 0 && off < len(text) && !utf8.RuneStart(text[off]) {
			off-- // never split a rune
		}
		if off > 0 && off < len(text) && text[off] == '\n' && text[off-1] == '\r' {
			off-- // never point inside CRLF
		}
		start, end := positionOf(text, off), positionOf(text, off)
		// Span one character when the error sits on one, so it is visible inline.
		if off < len(text) && separator(text, off) == 0 {
			_, size := utf8.DecodeRuneInString(text[off:])
			end = positionOf(text, off+size)
		}
		entry.diagnostics = append(entry.diagnostics, Diagnostic{
			Path: rel, Range: &Range{Start: start, End: end}, Severity: "error",
			Message: shorten(e.Msg, 2048), Source: "go syntax", Freshness: freshness,
			Basis: SavedBasis{Kind: "saved", Revision: revision},
		})
	}
	return entry
}

const goSyntaxNotice = "Go syntax only (go/parser, no type checking or build), saved files on disk; open drafts are not analyzed."

func (g *GoSyntax) result() CheckerResult {
	out := CheckerResult{Diagnostics: []Diagnostic{}, Roots: []string{""}, Replace: true, Availability: "available", Phase: "idle", Freshness: "uncertain", Notice: goSyntaxNotice}
	keys := make([]string, 0, len(g.cache))
	for k := range g.cache {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		e := g.cache[k]
		out.Partial = out.Partial || e.partial
		out.Diagnostics = append(out.Diagnostics, e.diagnostics...)
	}
	if g.walkPartial || g.overflow {
		out.Partial = true
	}
	if out.Partial {
		out.Notice = goSyntaxNotice + " Some files or directories were skipped by limits; coverage is partial."
	}
	return out
}

// HasGoSources is a bounded, metadata-only probe used during discovery.
func HasGoSources(root AuthorizedRoot) bool {
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return false
	}
	defer func() { _ = r.Close() }()
	g := NewGoSyntax(root, GoSyntaxLimits{Entries: 4096})
	_, files, _ := g.walk(r)
	return len(files) > 0
}
