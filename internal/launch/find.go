package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ErrAgentNotFound means no CLI for the agent is installed where the app
// looks for one.
var ErrAgentNotFound = errors.New("agent CLI not found")

// Executable is an agent CLI found on this machine.
type Executable struct {
	Path string
	// OnPath is true when PATH resolves the bare name to Path, so a copied
	// command can use the name instead.
	OnPath bool
	// Args are defaults to insert before the user's arguments on interactive
	// launches. Noninteractive callers use Path only.
	Args []string
}

// Find locates the CLI for an agent command name ("claude", "codex" or
// "opencode"). On Windows it also looks where the agents' installers, desktop
// apps and editor extensions keep their own copy, because desktop-only users
// rarely have one on PATH. It searches on every call: updates rename the
// versioned folders.
func Find(name string) (Executable, error) {
	return finder{
		goos:     runtime.GOOS,
		goarch:   runtime.GOARCH,
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
	}.find(name)
}

type finder struct {
	goos, goarch string
	getenv       func(string) string
	lookPath     func(string) (string, error)
}

func (f finder) find(name string) (Executable, error) {
	if path, err := f.lookPath(name); err == nil && !f.desktopAlias(name, path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return Executable{Path: path, OnPath: true, Args: f.bundleArgs(name, path)}, nil
	}
	if f.goos == "windows" {
		for _, loc := range windowsLocations(name, f.goarch) {
			base := f.getenv(loc.env)
			if base == "" {
				continue
			}
			if path := newestFile(expand(base, loc.parts)); path != "" {
				return Executable{Path: path, Args: slices.Clone(loc.args)}, nil
			}
		}
	}
	return Executable{}, fmt.Errorf("%w: %s", ErrAgentNotFound, installHint(name))
}

// bundleArgs also recognizes a bundled CLI put on PATH. Reuse the lookup
// locations, resolving short names before comparing the paths on Windows.
func (f finder) bundleArgs(name, path string) []string {
	if f.goos != "windows" || name != "codex" {
		return nil
	}
	path = resolvedPath(path)
	for _, loc := range windowsLocations(name, f.goarch) {
		if len(loc.args) == 0 {
			continue
		}
		base := f.getenv(loc.env)
		if base == "" {
			continue
		}
		for _, candidate := range expand(base, loc.parts) {
			if strings.EqualFold(path, resolvedPath(candidate)) {
				return slices.Clone(loc.args)
			}
		}
	}
	return nil
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// desktopAlias reports the app execution alias Claude Desktop installs as
// WindowsApps\Claude.exe. It is early on PATH and opens the desktop app, not
// Claude Code.
func (f finder) desktopAlias(name, path string) bool {
	if f.goos != "windows" || name != "claude" {
		return false
	}
	local := f.getenv("LOCALAPPDATA")
	return local != "" && strings.EqualFold(filepath.Clean(filepath.Dir(path)), filepath.Join(local, "Microsoft", "WindowsApps"))
}

// location is a path below a per-user directory variable. Parts may hold
// filepath.Match wildcards; they match one directory entry each.
type location struct {
	env   string
	parts []string
	args  []string
}

func loc(env string, parts ...string) location { return location{env: env, parts: parts} }

// Codex's desktop/editor copies can run the TUI but may lack the package
// needed to auto-start its daemon. A per-launch config override also works
// with older copies that don't recognize --no-daemon.
var codexBundleArgs = []string{"-c", "features.daemon_auto_start=false"}

func codexBundle(env string, parts ...string) location {
	return location{env: env, parts: parts, args: codexBundleArgs}
}

// windowsLocations lists where agent CLIs live besides PATH, in order of
// preference: standalone installs, then desktop apps, then editor
// extensions. Codex's bundled copies need daemon auto-start disabled for
// interactive launches; --version alone doesn't verify package completeness.
// Claude Desktop downloads claude.exe into its user-data directory
// (documented for the standard, MSIX-redirected and 3P installs).
func windowsLocations(name, goarch string) []location {
	var locs []location
	switch name {
	case "claude":
		locs = append(locs,
			loc("USERPROFILE", ".local", "bin", "claude.exe"),
			loc("APPDATA", "npm", "claude.cmd"),
			loc("APPDATA", "Claude", "claude-code", "*", "claude.exe"),
			loc("LOCALAPPDATA", "Packages", "Claude_*", "LocalCache", "Roaming", "Claude", "claude-code", "*", "claude.exe"),
			loc("LOCALAPPDATA", "Claude-3p", "claude-code", "*", "claude.exe"),
		)
		for _, editor := range editorDirs {
			locs = append(locs, loc("USERPROFILE", editor, "extensions", "anthropic.claude-code-*-win32-*", "resources", "native-binary", "claude.exe"))
		}
	case "codex":
		locs = append(locs,
			loc("APPDATA", "npm", "codex.cmd"),
			codexBundle("LOCALAPPDATA", "OpenAI", "Codex", "bin", "*", "codex.exe"),
		)
		arch := "x86_64"
		if goarch == "arm64" {
			arch = "aarch64"
		}
		for _, editor := range editorDirs {
			locs = append(locs, codexBundle("USERPROFILE", editor, "extensions", "openai.chatgpt-*-win32-*", "bin", "windows-"+arch, "codex.exe"))
		}
	case "opencode":
		locs = append(locs,
			loc("USERPROFILE", ".opencode", "bin", "opencode.exe"),
			loc("APPDATA", "npm", "opencode.cmd"),
			loc("LOCALAPPDATA", "Programs", "@opencodedesktop", "resources", "opencode-cli.exe"),
		)
	}
	return locs
}

// editorDirs are the per-user directories of VS Code and its forks that
// install the same extensions.
var editorDirs = []string{".vscode", ".vscode-insiders", ".cursor"}

// expand returns the paths below dir matching parts. Wildcards are matched
// against directory entries, never against dir itself, so a user directory
// containing glob characters is still found.
func expand(dir string, parts []string) []string {
	if len(parts) == 0 {
		return []string{dir}
	}
	if !strings.ContainsAny(parts[0], "*?[") {
		return expand(filepath.Join(dir, parts[0]), parts[1:])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if ok, _ := filepath.Match(parts[0], e.Name()); ok {
			out = append(out, expand(filepath.Join(dir, e.Name()), parts[1:])...)
		}
	}
	return out
}

// newestFile returns the most recently modified regular file of paths, so
// the latest of several installed versions wins.
func newestFile(paths []string) string {
	var best string
	var bestInfo os.FileInfo
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if bestInfo == nil || info.ModTime().After(bestInfo.ModTime()) {
			best, bestInfo = p, info
		}
	}
	return best
}

func installHint(name string) string {
	switch name {
	case "claude":
		return "Claude Code was not found. Install Claude Code, Claude Desktop or the Claude Code extension for VS Code, then try again"
	case "codex":
		return "Codex was not found. Install the Codex CLI, the Codex desktop app or the Codex extension for VS Code, then try again"
	case "opencode":
		return "OpenCode was not found. Install OpenCode or OpenCode Desktop, then try again"
	}
	return fmt.Sprintf("%s was not found on PATH", name)
}
