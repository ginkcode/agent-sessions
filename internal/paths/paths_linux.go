//go:build linux

package paths

import (
	"path/filepath"
)

// FromEnv resolves roots using the provided getenv function and user home directory.
// Relative or empty environment variable values are ignored per the XDG specification.
func FromEnv(getenv func(string) string, home string) Roots {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	claude := getenv("CLAUDE_CONFIG_DIR")
	if !filepath.IsAbs(claude) {
		claude = filepath.Join(home, ".claude")
	} else {
		claude = filepath.Clean(claude)
	}

	codex := getenv("CODEX_HOME")
	if !filepath.IsAbs(codex) {
		codex = filepath.Join(home, ".codex")
	} else {
		codex = filepath.Clean(codex)
	}

	dataHome := getenv("XDG_DATA_HOME")
	var openCodeData string
	var appData string
	if !filepath.IsAbs(dataHome) {
		openCodeData = filepath.Join(home, ".local", "share", "opencode")
		appData = filepath.Join(home, ".local", "share", "agent-sessions")
	} else {
		openCodeData = filepath.Join(filepath.Clean(dataHome), "opencode")
		appData = filepath.Join(filepath.Clean(dataHome), "agent-sessions")
	}

	cacheHome := getenv("XDG_CACHE_HOME")
	var cache string
	if !filepath.IsAbs(cacheHome) {
		cache = filepath.Join(home, ".cache", "agent-sessions")
	} else {
		cache = filepath.Join(filepath.Clean(cacheHome), "agent-sessions")
	}

	configHome := getenv("XDG_CONFIG_HOME")
	var config string
	if !filepath.IsAbs(configHome) {
		config = filepath.Join(home, ".config", "agent-sessions")
	} else {
		config = filepath.Join(filepath.Clean(configHome), "agent-sessions")
	}

	return Roots{
		Claude:       claude,
		Codex:        codex,
		OpenCodeData: openCodeData,
		Cache:        cache,
		Config:       config,
		Data:         appData,
	}
}
