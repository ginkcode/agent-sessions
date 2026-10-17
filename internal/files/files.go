// Package files lists and reads the files of a project directory for the
// read-only file viewer.
package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxFiles caps a listing so a huge tree cannot flood the UI.
const MaxFiles = 20000

// MaxReadBytes is the largest file Read returns the contents of.
const MaxReadBytes = 2 << 20

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
}

// Content is one file read for display.
type Content struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Text string `json:"text,omitempty"`
	// Binary is set when the file is not UTF-8 text; Text is then empty.
	Binary bool `json:"binary,omitempty"`
	// TooLarge is set when the file exceeds MaxReadBytes; Text is then empty.
	TooLarge bool `json:"tooLarge,omitempty"`
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
		out := Listing{Root: root, Files: files, Git: true}
		if len(out.Files) > MaxFiles {
			out.Files, out.Truncated = out.Files[:MaxFiles], true
		}
		return out, nil
	}
	return walk(ctx, root)
}

// gitFiles lists tracked and untracked, not ignored, files under root.
func gitFiles(ctx context.Context, root string) ([]string, bool) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard") //nolint:gosec
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if cmd.Run() != nil {
		return nil, false
	}
	seen := make(map[string]bool)
	files := []string{}
	for name := range strings.SplitSeq(stdout.String(), "\x00") {
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
// binary or larger than MaxReadBytes.
func Read(root, rel string) (Content, error) {
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
	return out, nil
}

// IsBinary reports whether data does not look like UTF-8 text: it has a NUL
// byte or is not valid UTF-8.
func IsBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}
