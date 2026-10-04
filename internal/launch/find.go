package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
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
// rarely have one on PATH. Elsewhere it looks in the usual install
// directories: an app started from a desktop menu or Finder never read the
// shell rc files that add them to PATH. It searches on every call: updates
// rename the versioned folders.
func Find(name string) (Executable, error) {
	home, _ := os.UserHomeDir()
	return finder{
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		getenv:     os.Getenv,
		lookPath:   exec.LookPath,
		home:       home,
		systemDirs: posixSystemDirs,
		mountInfo:  func() ([]byte, error) { return os.ReadFile("/proc/self/mountinfo") },
	}.find(name)
}

type finder struct {
	goos, goarch string
	getenv       func(string) string
	lookPath     func(string) (string, error)
	// home and systemDirs are searched outside Windows.
	home       string
	systemDirs []string
	// mountInfo reads /proc/self/mountinfo inside WSL. Nil, like a failed
	// read, leaves only the default /mnt/<letter> drive layout to go by.
	mountInfo func() ([]byte, error)
}

func (f finder) find(name string) (Executable, error) {
	onWindows := f.wslWindowsDrive()
	path, err := f.lookPath(name)
	var windowsCopy string
	if err == nil && onWindows(path) {
		windowsCopy = path
	} else if err == nil && !f.desktopAlias(name, path) {
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
	} else {
		for _, dir := range f.posixDirs(name) {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
				if onWindows(p) {
					if windowsCopy == "" {
						windowsCopy = p
					}
					continue
				}
				return Executable{Path: p}, nil
			}
		}
	}
	if windowsCopy != "" {
		return Executable{}, fmt.Errorf("%w: %s is a Windows program; install %s inside the WSL distribution", ErrAgentNotFound, windowsCopy, name)
	}
	return Executable{}, fmt.Errorf("%w: %s", ErrAgentNotFound, f.installHint(name))
}

// wslWindowsDrive returns the test for a path on a mounted Windows drive,
// which reports false everywhere but inside WSL. WSL appends the Windows
// PATH, so a distro whose login PATH lacks the agent would otherwise find the
// Windows copy and run it on the distro's files; the install directories are
// searched instead, and skip such copies too.
//
// A path is judged by where its symlinks lead, on the mount that holds it:
// drvfs, which WSL 2 serves over 9p, is a Windows drive wherever wsl.conf's
// automount root puts it, and any other mount, even one at /mnt/d, is Linux.
// Without the mount table, a path below /mnt/<letter>/ counts as a drive.
func (f finder) wslWindowsDrive() func(string) bool {
	if f.goos != "linux" || f.getenv("WSL_DISTRO_NAME") == "" {
		return func(string) bool { return false }
	}
	var mounts []mount
	if f.mountInfo != nil {
		if data, err := f.mountInfo(); err == nil {
			mounts = parseMountInfo(data)
		}
	}
	return func(file string) bool {
		if !path.IsAbs(file) {
			if abs, err := filepath.Abs(file); err == nil {
				file = abs
			}
		}
		if resolved, err := filepath.EvalSymlinks(file); err == nil {
			file = resolved
		}
		file = path.Clean(filepath.ToSlash(file))
		if len(mounts) == 0 {
			rest, ok := strings.CutPrefix(file, "/mnt/")
			return ok && len(rest) >= 2 && rest[1] == '/'
		}
		return onWindowsMount(mounts, file)
	}
}

// mount is one line of /proc/self/mountinfo.
type mount struct {
	point   string
	windows bool
}

// parseMountInfo reads the mount points and whether each is a Windows drive:
// drvfs under WSL 1, 9p with aname=drvfs under WSL 2. Malformed lines are
// skipped.
func parseMountInfo(data []byte) []mount {
	var out []mount
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, " ")
		// ID parent dev root point options [optional...] - fstype source super
		sep := slices.Index(fields, "-")
		if sep < 6 || len(fields) < sep+3 {
			continue
		}
		fstype := fields[sep+1]
		windows := fstype == "drvfs"
		if fstype == "9p" && len(fields) > sep+3 {
			for _, opt := range strings.FieldsFunc(fields[sep+3], func(r rune) bool { return r == ',' || r == ';' }) {
				if opt == "aname=drvfs" {
					windows = true
				}
			}
		}
		out = append(out, mount{point: unescapeMount(fields[4]), windows: windows})
	}
	return out
}

// unescapeMount decodes the \ooo escapes mountinfo writes for space, tab,
// newline and backslash in a mount point.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// onWindowsMount reports whether the innermost mount holding path is a
// Windows drive. Of mounts on the same point, the last listed is on top.
func onWindowsMount(mounts []mount, file string) bool {
	best, windows := -1, false
	for _, m := range mounts {
		p := path.Clean(m.point)
		if p != "/" && file != p && !strings.HasPrefix(file, p+"/") {
			continue
		}
		if len(p) >= best {
			best, windows = len(p), m.windows
		}
	}
	return windows
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

// posixInstallDirs lists where agent CLIs install themselves outside the
// system PATH, relative to the home directory: their installers, npm's
// user prefix, bun and volta. The OpenCode installer adds ~/.opencode/bin
// to PATH in the shell rc files only.
var posixInstallDirs = map[string][]string{
	"claude":   {".local/bin", ".claude/local", ".npm-global/bin", ".bun/bin", ".volta/bin"},
	"opencode": {".opencode/bin", ".bun/bin", ".local/bin", ".npm-global/bin"},
	"codex":    {".local/bin", ".npm-global/bin", ".bun/bin", ".volta/bin"},
}

// posixSystemDirs are absolute install locations shared by all CLIs.
var posixSystemDirs = []string{"/usr/local/bin", "/opt/homebrew/bin", "/home/linuxbrew/.linuxbrew/bin"}

// posixDirs returns the directories to search for name, in order: a custom
// OpenCode install dir, the per-CLI home dirs, the system dirs, then nvm's
// Node versions, the newest first.
func (f finder) posixDirs(name string) []string {
	var dirs []string
	if name == "opencode" {
		if d := f.getenv("OPENCODE_INSTALL_DIR"); d != "" {
			dirs = append(dirs, d)
		}
	}
	if f.home != "" {
		for _, rel := range posixInstallDirs[name] {
			dirs = append(dirs, filepath.Join(f.home, rel))
		}
	}
	dirs = append(dirs, f.systemDirs...)
	if f.home != "" {
		nvm := expand(filepath.Join(f.home, ".nvm", "versions", "node"), []string{"*", "bin"})
		sort.Sort(sort.Reverse(sort.StringSlice(nvm)))
		dirs = append(dirs, nvm...)
	}
	return dirs
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

var agentLabels = map[string]string{"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode"}

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

func (f finder) installHint(name string) string {
	if f.goos != "windows" {
		if label, ok := agentLabels[name]; ok {
			return label + " was not found on PATH or in its usual install locations. Install it, then try again"
		}
		return fmt.Sprintf("%s was not found on PATH", name)
	}
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
