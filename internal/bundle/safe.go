package bundle

import (
	"fmt"
	"strings"
)

// safeID mirrors internal/manage isSafeID: a conservative charset so an ID
// can never be a path or an option. A Claude subagent ID is the one exception,
// exactly "<parent-uuid>/agent-<agent-id>": one slash, in a fixed position,
// with both halves themselves safe. Anything else containing a slash (extra
// slashes, a leading slash, "..") is still rejected.
func safeID(id string) bool {
	if parent, agent, ok := strings.Cut(id, "/"); ok {
		return safeIDPart(parent) && strings.HasPrefix(agent, "agent-") && safeIDPart(agent)
	}
	return safeIDPart(id)
}

func safeIDPart(id string) bool {
	if id == "" || len(id) > 128 || strings.HasPrefix(id, "-") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func safeAgent(agent string) bool {
	switch agent {
	case "claude-code", "codex", "opencode":
		return true
	default:
		return false
	}
}

// safeRel reports whether rel is a relative path that stays inside its root.
func safeRel(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return "", fmt.Errorf("absolute path")
	}
	// Windows drive prefix.
	if len(rel) >= 2 && rel[1] == ':' {
		return "", fmt.Errorf("absolute path")
	}
	// Reject traversal before cleaning: collapsing ".." would hide it.
	slash := strings.ReplaceAll(rel, "\\", "/")
	for _, part := range strings.Split(slash, "/") {
		if part == ".." {
			return "", fmt.Errorf("path escapes root")
		}
	}
	cleaned := pathClean(slash)
	if cleaned == "" {
		return "", fmt.Errorf("empty path")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("path escapes root")
		}
	}
	return cleaned, nil
}

// safeZipName reports whether a zip entry name is one of the three well-known
// files or a native/<agent>/<rel> path.
func safeZipName(name string) error {
	if name == "" {
		return fmt.Errorf("empty name")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("absolute name %q", name)
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("backslash in name %q", name)
	}
	if strings.Contains(name, "//") {
		return fmt.Errorf("empty segment in name %q", name)
	}
	cleaned := pathClean(name)
	if cleaned != name {
		return fmt.Errorf("name %q is not clean", name)
	}
	switch name {
	case "manifest.json", "transcript.json", "handoff.md":
		return nil
	}
	const prefix = "native/"
	if !strings.HasPrefix(name, prefix) {
		return fmt.Errorf("unexpected entry %q", name)
	}
	rest := strings.TrimPrefix(name, prefix)
	agent, rel, ok := strings.Cut(rest, "/")
	if !ok || rel == "" {
		return fmt.Errorf("native entry %q missing agent or path", name)
	}
	if !safeAgent(agent) {
		return fmt.Errorf("native entry %q has unknown agent", name)
	}
	if _, err := safeRel(rel); err != nil {
		return fmt.Errorf("native entry %q: %w", name, err)
	}
	return nil
}
