package pathutil

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RepoInfo describes the repository containing a directory.
type RepoInfo struct {
	// Toplevel is the worktree top: the directory containing .git.
	Toplevel string
	// MainRoot is the main repository root. It equals Toplevel unless the
	// worktree is a linked worktree of another repository.
	MainRoot string
}

type cacheEntry struct {
	info  RepoInfo
	found bool
}

// GitResolver resolves directories to their repository roots without invoking
// the git binary. Results are memoized per directory, including misses.
type GitResolver struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
	hits  int
}

// NewGitResolver returns an empty resolver.
func NewGitResolver() *GitResolver {
	return &GitResolver{cache: make(map[string]cacheEntry)}
}

// Hits returns the number of cache lookups that found a memoized entry.
func (g *GitResolver) Hits() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hits
}

// CacheLen returns the number of memoized directories in the resolver.
func (g *GitResolver) CacheLen() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.cache)
}

func (g *GitResolver) getCached(p string) (cacheEntry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cache == nil {
		g.cache = make(map[string]cacheEntry)
	}
	entry, ok := g.cache[p]
	if ok {
		g.hits++
	}
	return entry, ok
}

func (g *GitResolver) putCached(paths []string, entry cacheEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cache == nil {
		g.cache = make(map[string]cacheEntry)
	}
	for _, p := range paths {
		if p != "" {
			g.cache[p] = entry
		}
	}
}

// Resolve walks up from dir to the filesystem root looking for a .git entry.
// It reports ok=false when dir is not inside a Git repository or worktree.
// A missing dir is handled by walking up from its nearest existing ancestor.
func (g *GitResolver) Resolve(dir string) (RepoInfo, bool) {
	if dir == "" {
		return RepoInfo{}, false
	}
	if !filepath.IsAbs(dir) {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	dir = filepath.Clean(dir)

	// Check if the directory itself is already cached.
	if entry, ok := g.getCached(dir); ok {
		return entry.info, entry.found
	}

	var visited []string
	start := dir

	if !Exists(start) {
		// Missing directory: collect missing components and find the nearest existing ancestor.
		visited = append(visited, start)
		ancestor := start
		for {
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				break
			}
			ancestor = parent
			if Exists(ancestor) {
				break
			}
			visited = append(visited, ancestor)
		}
		if Exists(ancestor) {
			normAncestor := NormalizeDir(ancestor)
			if normAncestor != ancestor {
				visited = append(visited, normAncestor)
			}
			start = normAncestor
		}
	} else {
		norm := NormalizeDir(start)
		if norm != start {
			visited = append(visited, start)
			start = norm
		}
	}

	// Walk up looking for .git.
	curr := start
	for {
		if entry, ok := g.getCached(curr); ok {
			g.putCached(visited, entry)
			return entry.info, entry.found
		}
		visited = append(visited, curr)

		if info, ok := inspectLevel(curr); ok {
			entry := cacheEntry{info: info, found: true}
			g.putCached(visited, entry)
			return info, true
		}

		parent := filepath.Dir(curr)
		if parent == curr {
			entry := cacheEntry{found: false}
			g.putCached(visited, entry)
			return RepoInfo{}, false
		}
		curr = parent
	}
}

// inspectLevel checks a single directory for a .git entry.
func inspectLevel(level string) (RepoInfo, bool) {
	gitPath := filepath.Join(level, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return RepoInfo{}, false
	}

	if fi.IsDir() {
		normLevel := NormalizeDir(level)
		return RepoInfo{
			Toplevel: normLevel,
			MainRoot: normLevel,
		}, true
	}

	// .git is a file (linked worktree or submodule pointer).
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return RepoInfo{}, false
	}
	gitdir, ok := parseGitdir(string(data))
	if !ok {
		return RepoInfo{}, false
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(level, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// Step 2: If <gitdir>/commondir exists, read it (usually ../..) and resolve against gitdir
	// to get the common .git dir. MainRoot is its parent.
	commondirPath := filepath.Join(gitdir, "commondir")
	if cdata, err := os.ReadFile(commondirPath); err == nil {
		common := strings.TrimSpace(string(cdata))
		if common != "" {
			var commonDir string
			if filepath.IsAbs(common) {
				commonDir = common
			} else {
				commonDir = filepath.Join(gitdir, common)
			}
			commonDir = NormalizeDir(commonDir)
			mainRoot := NormalizeDir(filepath.Dir(commonDir))
			return RepoInfo{
				Toplevel: NormalizeDir(level),
				MainRoot: mainRoot,
			}, true
		}
	}

	// Step 3: Otherwise MainRoot = level (submodule case).
	normLevel := NormalizeDir(level)
	return RepoInfo{
		Toplevel: normLevel,
		MainRoot: normLevel,
	}, true
}

// parseGitdir extracts the path from a ".git" pointer file.
func parseGitdir(data string) (string, bool) {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
			p := strings.TrimSpace(rest)
			if p != "" {
				return p, true
			}
		}
	}
	return "", false
}
