// Package files lists and reads the files of a project directory for the
// read-only file viewer.
package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxFiles caps a listing so a huge tree cannot flood the UI.
const MaxFiles = 20000

// MaxReadBytes is the largest text file Read returns the contents of. The
// viewer draws every line, so much larger files are slow to show; and JSON
// can grow text up to 6 times (\u003c for <), which this keeps under the
// RPC frame limit.
const MaxReadBytes = 5 << 20

// MaxImageBytes is the largest image Read returns, base64 encoded; it stays
// well under the RPC frame limit once encoded.
const MaxImageBytes = 20 << 20

// imageTypes are the image files Read returns for display, by extension.
// SVG is text and read as such.
var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".ico": "image/x-icon", ".avif": "image/avif",
}

// ErrOutsideRoot reports a path that leaves the project directory.
var ErrOutsideRoot = errors.New("path is outside the directory")

// Listing is the files of a directory, as slash-separated paths relative to
// it, sorted.
type Listing struct {
	Root      string   `json:"root"`
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated,omitempty"`
	// Git reports that the list came from git, so ignored files are left out.
	Git bool `json:"git,omitempty"`
	// Status is the git status of each changed file, by path: see the
	// Status constants. Deleted files are in Status but not in Files.
	Status map[string]Status `json:"status,omitempty"`
	// Ignored are files .gitignore leaves out, such as .env, so a search
	// can still find them. Ignored folders (node_modules, build output)
	// are not looked into, so only files outside them are here.
	Ignored []string `json:"ignored,omitempty"`
}

// Status is a file's git status, as VS Code's file tree shows it.
type Status string

const (
	Modified  Status = "M"
	Added     Status = "A"
	Untracked Status = "U"
	Deleted   Status = "D"
	Renamed   Status = "R"
	Conflict  Status = "C"
)

// LineChange is a run of lines that differs from the last commit, in the
// file's current line numbers (1-based). A deletion has Count 0 and Start
// the line after which lines were removed (0: before the first line).
type LineChange struct {
	Kind  string `json:"kind"` // "added", "modified" or "deleted"
	Start int    `json:"start"`
	Count int    `json:"count"`
	// Removed is the text of the lines this change replaced or removed, as
	// they were at the last commit, for the diff view.
	Removed []string `json:"removed,omitempty"`
}

// Content is one file read for display.
type Content struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Text string `json:"text,omitempty"`
	// Binary is set when the file is not UTF-8 text; Text is then empty.
	Binary bool `json:"binary,omitempty"`
	// TooLarge is set when the file exceeds MaxReadBytes (MaxImageBytes for
	// an image); Text is then empty.
	TooLarge bool `json:"tooLarge,omitempty"`
	// Image is the MIME type of an image file (see imageTypes), and Data its
	// bytes, base64 encoded. Binary is set too.
	Image string `json:"image,omitempty"`
	Data  string `json:"data,omitempty"`
	// Changes are the lines that differ from the last commit, for a text
	// file in a git work tree.
	Changes []LineChange `json:"changes,omitempty"`
	// Revision identifies the exact bytes of a text file returned in Text
	// (see revisionOf), so a later save can tell whether the file changed.
	// It is empty when Text is not returned.
	Revision string `json:"revision,omitempty"`
	// Edit tells whether a text file may be edited and the format a save
	// must keep. It is nil when Text is not returned.
	Edit *EditInfo `json:"edit,omitempty"`
}

// skipDirs are left out of a listing made without git: dependencies, build
// output and tool state that nobody browses as source.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true,
	".venv": true, "venv": true, "__pycache__": true, ".mypy_cache": true, ".pytest_cache": true,
	".tox": true, "dist": true, "build": true, "target": true, ".next": true, ".nuxt": true,
	".cache": true, ".gradle": true, ".idea": true, ".terraform": true, "coverage": true,
}

// List returns the files under root. Inside a git work tree it asks git, so
// files ignored by .gitignore are left out; elsewhere it walks the tree,
// skipping dependency and build directories.
func List(ctx context.Context, root string) (Listing, error) {
	info, err := os.Stat(root)
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, fmt.Errorf("%s is not a directory", root)
	}
	if files, ok := gitFiles(ctx, root); ok {
		out := Listing{Root: root, Files: files, Git: true, Status: gitStatus(ctx, root)}
		if len(out.Files) > MaxFiles {
			out.Files, out.Truncated = out.Files[:MaxFiles], true
		}
		out.Ignored = gitIgnored(ctx, root, MaxFiles-len(out.Files))
		return out, nil
	}
	return walk(ctx, root)
}

// runGit runs git in root and returns its output.
func runGit(ctx context.Context, root string, args ...string) ([]byte, bool) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	argv := append([]string{"-C", root, "--literal-pathspecs", "-c", "core.quotepath=off"}, args...)
	cmd := exec.CommandContext(ctx, git, argv...) //nolint:gosec
	hideConsole(cmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if cmd.Run() != nil {
		return nil, false
	}
	return stdout.Bytes(), true
}

// gitFiles lists tracked and untracked, not ignored, files under root.
func gitFiles(ctx context.Context, root string) ([]string, bool) {
	out, ok := runGit(ctx, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if !ok {
		return nil, false
	}
	seen := make(map[string]bool)
	files := []string{}
	for name := range strings.SplitSeq(string(out), "\x00") {
		// A deleted tracked file is still listed until the change is staged.
		if name == "" || seen[name] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			continue
		}
		seen[name] = true
		files = append(files, name)
	}
	slices.Sort(files)
	return files, true
}

// gitIgnored lists the ignored files under root, at most limit of them.
// With --directory git reports an ignored folder as one "dir/" entry
// without walking it; those entries are dropped.
func gitIgnored(ctx context.Context, root string, limit int) []string {
	out, ok := runGit(ctx, root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory", "--no-empty-directory")
	if !ok {
		return nil
	}
	var ignored []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if len(ignored) >= limit {
			break
		}
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		ignored = append(ignored, name)
	}
	slices.Sort(ignored)
	return ignored
}

// gitStatus returns the status of the changed files under root, by path
// relative to root. It is nil when git cannot tell.
func gitStatus(ctx context.Context, root string) map[string]Status {
	prefix, ok := runGit(ctx, root, "rev-parse", "--show-prefix")
	if !ok {
		return nil
	}
	out, ok := runGit(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
	if !ok {
		return nil
	}
	return parseStatus(string(out), strings.TrimSpace(string(prefix)))
}

// parseStatus reads `git status --porcelain=v1 -z` output. Its paths are
// from the repository top; prefix (root's place in it, ending in /) is cut
// from them.
func parseStatus(out, prefix string) map[string]Status {
	status := make(map[string]Status)
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		x, y, path := entry[0], entry[1], entry[3:]
		// A rename or copy is followed by the path it came from.
		if x == 'R' || x == 'C' {
			i++
		}
		rel, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		status[rel] = statusOf(x, y)
	}
	return status
}

// statusOf picks the one letter VS Code would show for an XY status.
func statusOf(x, y byte) Status {
	switch {
	case x == '?' && y == '?':
		return Untracked
	case x == 'U' || y == 'U' || x == 'A' && y == 'A' || x == 'D' && y == 'D':
		return Conflict
	case y == 'D' || x == 'D' && y == ' ':
		return Deleted
	case x == 'A':
		return Added
	case x == 'R':
		return Renamed
	}
	return Modified
}

// gitChanges returns the lines of rel that differ from the last commit, or
// nil outside a work tree, before the first commit, or for an unchanged file.
func gitChanges(ctx context.Context, root, rel string) []LineChange {
	out, ok := runGit(ctx, root, "diff", "--no-color", "--no-ext-diff", "-U0", "HEAD", "--", rel)
	if !ok {
		return nil
	}
	return parseHunks(string(out))
}

// parseHunks reads a -U0 diff: each @@ -a,b +c,d @@ header and the lines
// it removed. "\ No newline at end of file" notes are skipped.
func parseHunks(diff string) []LineChange {
	var changes []LineChange
	var cur *LineChange
	inHunk := false
	for line := range strings.SplitSeq(diff, "\n") {
		if rest, ok := strings.CutPrefix(line, "@@ -"); ok {
			inHunk = false
			oldPart, rest, ok := strings.Cut(rest, " +")
			if !ok {
				continue
			}
			newPart, _, ok := strings.Cut(rest, " @@")
			if !ok {
				continue
			}
			_, oldCount := hunkRange(oldPart)
			newStart, newCount := hunkRange(newPart)
			c := LineChange{Kind: "modified", Start: newStart, Count: newCount}
			switch {
			case oldCount == 0:
				c.Kind = "added"
			case newCount == 0:
				c = LineChange{Kind: "deleted", Start: newStart}
			}
			changes = append(changes, c)
			cur = &changes[len(changes)-1]
			inHunk = true
			continue
		}
		if inHunk && cur != nil && strings.HasPrefix(line, "-") {
			cur.Removed = append(cur.Removed, strings.TrimSuffix(line[1:], "\r"))
		}
	}
	return changes
}

// hunkRange reads "start,count" or "start" (count 1).
func hunkRange(s string) (start, count int) {
	a, b, hasCount := strings.Cut(s, ",")
	start, _ = strconv.Atoi(a)
	count = 1
	if hasCount {
		count, _ = strconv.Atoi(b)
	}
	return start, count
}

func walk(ctx context.Context, root string) (Listing, error) {
	out := Listing{Root: root, Files: []string{}}
	errFull := errors.New("full")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped, not fatal.
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if len(out.Files) >= MaxFiles {
			out.Truncated = true
			return errFull
		}
		rel, err := filepath.Rel(root, p)
		if err == nil {
			out.Files = append(out.Files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFull) {
		return Listing{}, err
	}
	slices.Sort(out.Files)
	return out, nil
}

// Resolve returns the file rel names under root. It refuses paths that leave
// root, including through a symlink.
func Resolve(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", ErrOutsideRoot
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	full := filepath.Join(root, clean)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(realRoot, realFull)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	return realFull, nil
}

// Read returns the contents of the file rel names under root, unless it is
// binary or larger than MaxReadBytes. A common image type is returned as
// base64 data instead, up to MaxImageBytes.
func Read(ctx context.Context, root, rel string) (Content, error) {
	full, err := Resolve(root, rel)
	if err != nil {
		return Content{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Content{}, err
	}
	if !info.Mode().IsRegular() {
		return Content{}, fmt.Errorf("%s is not a regular file", rel)
	}
	out := Content{Path: filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel))), Size: info.Size()}
	if mime, ok := imageTypes[strings.ToLower(filepath.Ext(full))]; ok {
		return readImage(f, out, mime)
	}
	if info.Size() > MaxReadBytes {
		out.TooLarge = true
		return out, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil {
		return Content{}, err
	}
	if len(data) > MaxReadBytes {
		out.TooLarge = true
		return out, nil
	}
	if IsBinary(data) {
		out.Binary = true
		return out, nil
	}
	out.Text = string(data)
	out.Revision = revisionOf(data)
	out.Edit = textEditInfo(data)
	out.Changes = gitChanges(ctx, root, out.Path)
	return out, nil
}

// readImage returns an image file's bytes, base64 encoded, unless it is
// larger than MaxImageBytes.
func readImage(f *os.File, out Content, mime string) (Content, error) {
	out.Binary = true
	if out.Size > MaxImageBytes {
		out.TooLarge = true
		return out, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil {
		return Content{}, err
	}
	if len(data) > MaxImageBytes {
		out.TooLarge = true
		return out, nil
	}
	out.Image = mime
	out.Data = base64.StdEncoding.EncodeToString(data)
	return out, nil
}

// IsBinary reports whether data does not look like UTF-8 text: it has a NUL
// byte or is not valid UTF-8.
func IsBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}
