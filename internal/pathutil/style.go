package pathutil

import (
	"path"
	"runtime"
	"strings"
)

// Session files record paths in the style of the machine that wrote them, so
// a Windows app shows POSIX paths from remote hosts and bundles, and a Linux
// app shows Windows ones. The functions in this file pick the path style from
// the path itself, never from the host OS. Use filepath only for paths that
// are opened on this machine.

// IsWindows reports whether p is written in Windows style: it starts with a
// drive letter (C:), a backslash (\\server\share, \\?\C:\, \dir), or it is a
// relative path that uses backslashes and no forward slashes (src\main.go).
// //server/share stays POSIX: Linux reads it as /server/share, and Windows
// records UNC paths with backslashes.
func IsWindows(p string) bool {
	if hasDrive(p) || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "//?/") {
		return true
	}
	return strings.Contains(p, `\`) && !strings.Contains(p, "/")
}

// IsAbs reports whether p is absolute in its own style: /x for POSIX, and
// C:\x or \\server\share\x for Windows.
func IsAbs(p string) bool {
	if !IsWindows(p) {
		return strings.HasPrefix(p, "/")
	}
	vol, rest := splitVolume(toBackslash(stripLongPrefix(p)))
	return vol != "" && (strings.HasPrefix(vol, `\\`) || strings.HasPrefix(rest, `\`))
}

// Clean returns the shortest equivalent of p in its own style. Windows paths
// use backslashes, lose the \\?\ long-path prefix, and get an upper-case drive
// letter, so the same directory recorded by different agents compares equal.
func Clean(p string) string {
	if p == "" {
		return ""
	}
	if !IsWindows(p) {
		return path.Clean(p)
	}
	vol, rest := splitVolume(toBackslash(stripLongPrefix(p)))
	if rest == "" {
		return vol
	}
	clean := path.Clean(strings.ReplaceAll(rest, `\`, "/"))
	if clean == "." && vol != "" || clean == "/" && strings.HasPrefix(vol, `\\`) {
		return vol
	}
	return vol + toBackslash(clean)
}

// Rel returns target relative to base when target is base itself ("") or
// lies inside it. Both must be absolute and of the same style; Windows paths
// compare case-insensitively. The result uses the style's separator.
func Rel(base, target string) (string, bool) {
	if !IsAbs(base) || !IsAbs(target) || IsWindows(base) != IsWindows(target) {
		return "", false
	}
	base, target = Clean(base), Clean(target)
	sep := Separator(base)
	equal := func(a, b string) bool { return a == b }
	if IsWindows(base) {
		equal = strings.EqualFold
	}
	if len(target) < len(base) || !equal(target[:len(base)], base) {
		return "", false
	}
	rest := target[len(base):]
	switch {
	case rest == "":
		return "", true
	case strings.HasSuffix(base, sep):
		return rest, true
	case strings.HasPrefix(rest, sep):
		return rest[1:], true
	}
	return "", false
}

// Separator returns the separator for p's style.
func Separator(p string) string {
	if IsWindows(p) {
		return `\`
	}
	return "/"
}

// isForeign reports whether p is absolute in the other OS's style, so it
// names nothing on this machine.
func isForeign(p string) bool {
	return IsAbs(p) && IsWindows(p) != (runtime.GOOS == "windows")
}

func hasDrive(p string) bool {
	return len(p) >= 2 && p[1] == ':' && ('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z')
}

func toBackslash(p string) string { return strings.ReplaceAll(p, "/", `\`) }

// stripLongPrefix removes the \\?\ prefix Windows uses for long paths:
// \\?\C:\x becomes C:\x and \\?\UNC\server\share becomes \\server\share.
func stripLongPrefix(p string) string {
	if rest, ok := strings.CutPrefix(toBackslash(p), `\\?\`); ok {
		if len(rest) >= 4 && strings.EqualFold(rest[:4], `UNC\`) {
			return `\\` + rest[4:]
		}
		return rest
	}
	return p
}

// splitVolume splits a backslashed Windows path into its volume (C: or
// \\server\share) and the rest. The drive letter is upper-cased.
func splitVolume(p string) (vol, rest string) {
	if hasDrive(p) {
		return strings.ToUpper(p[:1]) + ":", p[2:]
	}
	if !strings.HasPrefix(p, `\\`) {
		return "", p
	}
	parts := strings.SplitN(p[2:], `\`, 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", p
	}
	vol = `\\` + parts[0] + `\` + parts[1]
	return vol, p[len(vol):]
}
